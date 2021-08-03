package postgres

import (
	"context"
	"fmt"
	"runtime"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v4/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/authzed/spicedb/internal/datastore"
	v0 "github.com/authzed/spicedb/pkg/proto/authzed/api/v0"
)

var relationNameKey = attribute.Key("authzed.com/spicedb/relationName")

const errUnableToQueryTuples = "unable to query tuples: %w"

var queryTuples = psql.Select(
	colNamespace,
	colObjectID,
	colRelation,
	colUsersetNamespace,
	colUsersetObjectID,
	colUsersetRelation,
).From(tableTuple)

func (pgd *pgDatastore) QueryTuples(namespace string, revision datastore.Revision) datastore.TupleQuery {
	return pgTupleQuery{
		commonTupleQuery: commonTupleQuery{
			dbpool: pgd.dbpool,
			query: queryTuples.
				Where(sq.Eq{colNamespace: namespace}).
				Where(sq.LtOrEq{colCreatedTxn: transactionFromRevision(revision)}).
				Where(sq.Or{
					sq.Eq{colDeletedTxn: liveDeletedTxnID},
					sq.Gt{colDeletedTxn: revision},
				}),
			tracerAttributes: []attribute.KeyValue{namespaceNameKey.String(namespace)},
		},
	}
}

type commonTupleQuery struct {
	dbpool *pgxpool.Pool
	query  sq.SelectBuilder

	tracerAttributes []attribute.KeyValue
}

type pgTupleQuery struct {
	commonTupleQuery
}

func (ctq commonTupleQuery) Limit(limit uint64) datastore.CommonTupleQuery {
	ctq.query = ctq.query.Limit(limit)
	return ctq
}

func (ptq pgTupleQuery) WithObjectID(objectID string) datastore.TupleQuery {
	ptq.query = ptq.query.Where(sq.Eq{colObjectID: objectID})
	return ptq
}

func (ptq pgTupleQuery) WithRelation(relation string) datastore.TupleQuery {
	ptq.query = ptq.query.Where(sq.Eq{colRelation: relation})
	ptq.tracerAttributes = append(ptq.tracerAttributes, relationNameKey.String(relation))
	return ptq
}

func (ptq pgTupleQuery) WithUserset(userset *v0.ObjectAndRelation) datastore.TupleQuery {
	ptq.query = ptq.query.Where(sq.Eq{
		colUsersetNamespace: userset.Namespace,
		colUsersetObjectID:  userset.ObjectId,
		colUsersetRelation:  userset.Relation,
	})
	return ptq
}

func (ptq pgTupleQuery) WithUsersets(usersets []*v0.ObjectAndRelation) datastore.TupleQuery {
	if len(usersets) == 0 {
		panic("Cannot send empty usersets into query")
	}

	var clause sq.Sqlizer = sq.Eq{
		colUsersetNamespace: usersets[0].Namespace,
		colUsersetObjectID:  usersets[0].ObjectId,
		colUsersetRelation:  usersets[0].Relation,
	}

	for _, userset := range usersets[1:] {
		clause = sq.Or{
			clause,
			sq.Eq{
				colUsersetNamespace: userset.Namespace,
				colUsersetObjectID:  userset.ObjectId,
				colUsersetRelation:  userset.Relation,
			},
		}
	}

	ptq.query = ptq.query.Where(clause)
	return ptq
}

func (ctq commonTupleQuery) Execute(ctx context.Context) (datastore.TupleIterator, error) {
	ctx, span := tracer.Start(ctx, "ExecuteTupleQuery")
	defer span.End()

	span.SetAttributes(ctq.tracerAttributes...)

	sql, args, err := ctq.query.ToSql()
	if err != nil {
		return nil, fmt.Errorf(errUnableToQueryTuples, err)
	}

	span.AddEvent("Query converted to SQL")

	rows, err := ctq.dbpool.Query(datastore.SeparateContextWithTracing(ctx), sql, args...)
	if err != nil {
		return nil, fmt.Errorf(errUnableToQueryTuples, err)
	}
	defer rows.Close()

	span.AddEvent("Query issued to SQL")

	var tuples []*v0.RelationTuple
	for rows.Next() {
		nextTuple := &v0.RelationTuple{
			ObjectAndRelation: &v0.ObjectAndRelation{},
			User: &v0.User{
				UserOneof: &v0.User_Userset{
					Userset: &v0.ObjectAndRelation{},
				},
			},
		}
		userset := nextTuple.User.GetUserset()
		err := rows.Scan(
			&nextTuple.ObjectAndRelation.Namespace,
			&nextTuple.ObjectAndRelation.ObjectId,
			&nextTuple.ObjectAndRelation.Relation,
			&userset.Namespace,
			&userset.ObjectId,
			&userset.Relation,
		)
		if err != nil {
			return nil, fmt.Errorf(errUnableToQueryTuples, err)
		}

		tuples = append(tuples, nextTuple)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errUnableToQueryTuples, err)
	}

	span.AddEvent("Tuples loaded", trace.WithAttributes(attribute.Int("tupleCount", len(tuples))))

	iter := datastore.NewSliceTupleIterator(tuples)

	runtime.SetFinalizer(iter, datastore.BuildFinalizerFunction(sql, args))

	return iter, nil
}
