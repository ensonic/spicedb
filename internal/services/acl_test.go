package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/authzed/spicedb/internal/datastore/memdb"
	"github.com/authzed/spicedb/internal/graph"
	tf "github.com/authzed/spicedb/internal/testfixtures"
	api "github.com/authzed/spicedb/pkg/REDACTEDapi/api"
	g "github.com/authzed/spicedb/pkg/graph"
	"github.com/authzed/spicedb/pkg/tuple"
	"github.com/authzed/spicedb/pkg/zookie"
)

var ONR = tuple.ObjectAndRelation

var testTimedeltas = []time.Duration{0, 1 * time.Second}

func TestRead(t *testing.T) {
	testCases := []struct {
		name         string
		filter       *api.RelationTupleFilter
		expectedCode codes.Code
		expected     []string
	}{
		{
			"namespace only",
			&api.RelationTupleFilter{Namespace: tf.DocumentNS.Name},
			codes.OK,
			[]string{
				"document:masterplan#parent@folder:strategy#...",
				"document:masterplan#owner@user:product_manager#...",
				"document:masterplan#viewer@user:eng_lead#...",
				"document:masterplan#parent@folder:plans#...",
				"document:healthplan#parent@folder:plans#...",
			},
		},
		{
			"namespace and object id",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				ObjectId:  "healthplan",
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_OBJECT_ID,
				},
			},
			codes.OK,
			[]string{
				"document:healthplan#parent@folder:plans#...",
			},
		},
		{
			"namespace and relation",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				Relation:  "parent",
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_RELATION,
				},
			},
			codes.OK,
			[]string{
				"document:masterplan#parent@folder:strategy#...",
				"document:masterplan#parent@folder:plans#...",
				"document:healthplan#parent@folder:plans#...",
			},
		},
		{
			"namespace and userset",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				Userset:   ONR("folder", "plans", "..."),
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_USERSET,
				},
			},
			codes.OK,
			[]string{
				"document:masterplan#parent@folder:plans#...",
				"document:healthplan#parent@folder:plans#...",
			},
		},
		{
			"multiple filters",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				ObjectId:  "masterplan",
				Userset:   ONR("folder", "plans", "..."),
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_USERSET,
					api.RelationTupleFilter_OBJECT_ID,
				},
			},
			codes.OK,
			[]string{
				"document:masterplan#parent@folder:plans#...",
			},
		},
		{
			"bad namespace",
			&api.RelationTupleFilter{Namespace: ""},
			codes.InvalidArgument,
			nil,
		},
		{
			"bad objectId",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				ObjectId:  "ma",
				Userset:   ONR("folder", "plans", "..."),
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_USERSET,
					api.RelationTupleFilter_OBJECT_ID,
				},
			},
			codes.InvalidArgument,
			nil,
		},
		{
			"bad object relation",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				Relation:  "ad",
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_RELATION,
				},
			},
			codes.InvalidArgument,
			nil,
		},
		{
			"bad userset",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				ObjectId:  "ma",
				Userset:   ONR("folder", "", "..."),
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_USERSET,
					api.RelationTupleFilter_OBJECT_ID,
				},
			},
			codes.InvalidArgument,
			nil,
		},
		{
			"nil argument required filter",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				Userset:   nil,
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_OBJECT_ID,
				},
			},
			codes.InvalidArgument,
			nil,
		},
		{
			"missing namespace",
			&api.RelationTupleFilter{
				Namespace: "doesnotexist",
			},
			codes.FailedPrecondition,
			nil,
		},
		{
			"missing relation",
			&api.RelationTupleFilter{
				Namespace: tf.DocumentNS.Name,
				Relation:  "invalidrelation",
				Filters: []api.RelationTupleFilter_Filter{
					api.RelationTupleFilter_RELATION,
				},
			},
			codes.FailedPrecondition,
			nil,
		},
	}

	for _, delta := range testTimedeltas {
		t.Run(fmt.Sprintf("fuzz%d", delta), func(t *testing.T) {
			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					require := require.New(t)

					srv, revision := newACLServicer(require, delta, memdb.DisableGC)

					resp, err := srv.Read(context.Background(), &api.ReadRequest{
						Tuplesets:  []*api.RelationTupleFilter{tc.filter},
						AtRevision: zookie.NewFromRevision(revision),
					})

					if tc.expectedCode == codes.OK {
						require.NoError(err)
						require.NotNil(resp.Revision)
						require.Len(resp.Tuplesets, 1)

						verifyTuples(tc.expected, resp.Tuplesets[0].Tuples, require)
					} else {
						requireGRPCStatus(tc.expectedCode, err, require)
					}
				})
			}
		})
	}
}

func TestReadBadZookie(t *testing.T) {
	require := require.New(t)

	srv, revision := newACLServicer(require, 0, 10*time.Millisecond)

	_, err := srv.Read(context.Background(), &api.ReadRequest{
		Tuplesets: []*api.RelationTupleFilter{
			{Namespace: tf.DocumentNS.Name},
		},
		AtRevision: zookie.NewFromRevision(revision),
	})
	require.NoError(err)

	_, err = srv.Read(context.Background(), &api.ReadRequest{
		Tuplesets: []*api.RelationTupleFilter{
			{Namespace: tf.DocumentNS.Name},
		},
		AtRevision: zookie.NewFromRevision(revision - 1),
	})
	require.NoError(err)

	// Wait until the gc window expires
	time.Sleep(20 * time.Millisecond)

	_, err = srv.Read(context.Background(), &api.ReadRequest{
		Tuplesets: []*api.RelationTupleFilter{
			{Namespace: tf.DocumentNS.Name},
		},
		AtRevision: zookie.NewFromRevision(revision),
	})
	require.NoError(err)

	_, err = srv.Read(context.Background(), &api.ReadRequest{
		Tuplesets: []*api.RelationTupleFilter{
			{Namespace: tf.DocumentNS.Name},
		},
		AtRevision: zookie.NewFromRevision(revision - 1),
	})
	requireGRPCStatus(codes.OutOfRange, err, require)

	_, err = srv.Read(context.Background(), &api.ReadRequest{
		Tuplesets: []*api.RelationTupleFilter{
			{Namespace: tf.DocumentNS.Name},
		},
		AtRevision: zookie.NewFromRevision(revision + 1),
	})
	requireGRPCStatus(codes.OutOfRange, err, require)
}

func TestWrite(t *testing.T) {
	require := require.New(t)

	srv, _ := newACLServicer(require, 0, memdb.DisableGC)

	toWriteStr := "document:totallynew#parent@folder:plans#..."
	toWrite := tuple.Scan(toWriteStr)
	require.NotNil(toWrite)

	resp, err := srv.Write(context.Background(), &api.WriteRequest{
		WriteConditions: []*api.RelationTuple{toWrite},
		Updates:         []*api.RelationTupleUpdate{tuple.Create(toWrite)},
	})
	require.Nil(resp)
	requireGRPCStatus(codes.FailedPrecondition, err, require)

	existing := tuple.Scan(tf.StandardTuples[0])
	require.NotNil(existing)

	resp, err = srv.Write(context.Background(), &api.WriteRequest{
		WriteConditions: []*api.RelationTuple{existing},
		Updates:         []*api.RelationTupleUpdate{tuple.Create(toWrite)},
	})
	require.NoError(err)
	require.NotNil(resp.Revision)
	require.NotZero(resp.Revision.Token)

	findWritten := []*api.RelationTupleFilter{
		{
			Namespace: "document",
			ObjectId:  "totallynew",
			Filters: []api.RelationTupleFilter_Filter{
				api.RelationTupleFilter_OBJECT_ID,
			},
		},
	}
	readBack, err := srv.Read(context.Background(), &api.ReadRequest{
		AtRevision: resp.Revision,
		Tuplesets:  findWritten,
	})
	require.NoError(err)
	require.Len(readBack.Tuplesets, 1)

	verifyTuples([]string{toWriteStr}, readBack.Tuplesets[0].Tuples, require)

	deleted, err := srv.Write(context.Background(), &api.WriteRequest{
		Updates: []*api.RelationTupleUpdate{tuple.Delete(toWrite)},
	})
	require.NoError(err)

	verifyMissing, err := srv.Read(context.Background(), &api.ReadRequest{
		AtRevision: deleted.Revision,
		Tuplesets:  findWritten,
	})
	require.NoError(err)
	require.Len(verifyMissing.Tuplesets, 1)

	verifyTuples(nil, verifyMissing.Tuplesets[0].Tuples, require)
}

func TestInvalidWriteArguments(t *testing.T) {
	testCases := []struct {
		name          string
		preconditions []string
		tuples        []string
	}{
		{
			"empty tuple",
			nil,
			[]string{":#@:#"},
		},
		{
			"bad precondition",
			[]string{":#@:#"},
			nil,
		},
		{
			"good precondition, short object ID",
			[]string{"document:newdoc#parent@folder:afolder#..."},
			[]string{"document:ab#parent@folder:afolder#..."},
		},
		{
			"bad precondition, good write",
			[]string{"document:newdoc#parent@folder:a#..."},
			[]string{"document:newdoc#parent@folder:afolder#..."},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)
			srv, _ := newACLServicer(require, 0, memdb.DisableGC)

			var preconditions []*api.RelationTuple
			for _, p := range tc.preconditions {
				preconditions = append(preconditions, tuple.Scan(p))
			}

			var mutations []*api.RelationTupleUpdate
			for _, tpl := range tc.tuples {
				mutations = append(mutations, tuple.Touch(tuple.Scan(tpl)))
			}

			_, err := srv.Write(context.Background(), &api.WriteRequest{
				WriteConditions: preconditions,
				Updates:         mutations,
			})
			requireGRPCStatus(codes.InvalidArgument, err, require)
		})
	}
}

func TestCheck(t *testing.T) {
	type checkTest struct {
		user       *api.ObjectAndRelation
		membership bool
	}

	testCases := []struct {
		start             *api.ObjectAndRelation
		expectedErrorCode codes.Code
		checkTests        []checkTest
	}{
		{
			ONR("document", "masterplan", "owner"),
			codes.OK,
			[]checkTest{
				{ONR("user", "product_manager", "..."), true},
				{ONR("user", "unknown", "..."), false},
				{ONR("user", "eng_lead", "..."), false},
				{ONR("user", "villain", "..."), false},
			},
		},
		{
			ONR("document", "masterplan", "viewer"),
			codes.OK,
			[]checkTest{
				{ONR("user", "product_manager", "..."), true},
				{ONR("user", "chief_financial_officer", "..."), true},
				{ONR("user", "villain", "..."), false},
			},
		},
		{
			ONR("document", "masterplan", "fakerelation"),
			codes.FailedPrecondition,
			[]checkTest{
				{ONR("user", "product_manager", "..."), false},
			},
		},
		{
			ONR("docs", "masterplan", "owner"),
			codes.FailedPrecondition,
			[]checkTest{
				{ONR("user", "product_manager", "..."), false},
			},
		},
		{
			ONR("document", "", "fakerelation"),
			codes.InvalidArgument,
			[]checkTest{
				{ONR("user", "product_manager", "..."), false},
			},
		},
		{
			ONR("document", "masterplan", "fakerelation"),
			codes.InvalidArgument,
			[]checkTest{
				{ONR("user", "", "..."), false},
			},
		},
	}

	for _, delta := range testTimedeltas {
		t.Run(fmt.Sprintf("fuzz%d", delta), func(t *testing.T) {
			for _, tc := range testCases {
				t.Run(tuple.StringONR(tc.start), func(t *testing.T) {
					for _, checkTest := range tc.checkTests {
						name := fmt.Sprintf(
							"%s=>%t",
							tuple.StringONR(checkTest.user),
							checkTest.membership,
						)
						t.Run(name, func(t *testing.T) {
							require := require.New(t)
							srv, revision := newACLServicer(require, delta, memdb.DisableGC)

							resp, err := srv.Check(context.Background(), &api.CheckRequest{
								TestUserset: tc.start,
								User: &api.User{
									UserOneof: &api.User_Userset{
										Userset: checkTest.user,
									},
								},
								AtRevision: zookie.NewFromRevision(revision),
							})

							ccResp, ccErr := srv.ContentChangeCheck(context.Background(), &api.ContentChangeCheckRequest{
								TestUserset: tc.start,
								User: &api.User{
									UserOneof: &api.User_Userset{
										Userset: checkTest.user,
									},
								},
							})

							if tc.expectedErrorCode == codes.OK {
								require.NoError(err)
								require.NoError(ccErr)
								require.NotNil(resp.Revision)
								require.NotNil(ccResp.Revision)
								require.NotEmpty(resp.Revision.Token)
								require.NotEmpty(ccResp.Revision.Token)
								require.Equal(checkTest.membership, resp.IsMember)
								require.Equal(checkTest.membership, ccResp.IsMember)
								if checkTest.membership {
									require.Equal(api.CheckResponse_MEMBER, resp.Membership)
									require.Equal(api.CheckResponse_MEMBER, ccResp.Membership)
								} else {
									require.Equal(api.CheckResponse_NOT_MEMBER, resp.Membership)
									require.Equal(api.CheckResponse_NOT_MEMBER, ccResp.Membership)
								}
							} else {
								requireGRPCStatus(tc.expectedErrorCode, err, require)
								requireGRPCStatus(tc.expectedErrorCode, ccErr, require)
							}
						})
					}
				})
			}
		})
	}
}

func TestExpand(t *testing.T) {
	testCases := []struct {
		start              *api.ObjectAndRelation
		expandRelatedCount int
		expectedErrorCode  codes.Code
	}{
		{ONR("document", "masterplan", "owner"), 1, codes.OK},
		{ONR("document", "masterplan", "viewer"), 7, codes.OK},
		{ONR("document", "masterplan", "fakerelation"), 0, codes.FailedPrecondition},
		{ONR("document", "", "owner"), 1, codes.InvalidArgument},
	}

	for _, delta := range testTimedeltas {
		t.Run(fmt.Sprintf("fuzz%d", delta/time.Millisecond), func(t *testing.T) {
			for _, tc := range testCases {
				t.Run(tuple.StringONR(tc.start), func(t *testing.T) {
					require := require.New(t)
					srv, revision := newACLServicer(require, delta, memdb.DisableGC)

					expanded, err := srv.Expand(context.Background(), &api.ExpandRequest{
						Userset:    tc.start,
						AtRevision: zookie.NewFromRevision(revision),
					})
					if tc.expectedErrorCode == codes.OK {
						require.NoError(err)
						require.NotNil(expanded.Revision)
						require.NotEmpty(expanded.Revision.Token)

						require.Equal(tc.expandRelatedCount, len(g.Simplify(expanded.TreeNode)))
					} else {
						requireGRPCStatus(tc.expectedErrorCode, err, require)
					}
				})
			}
		})
	}
}

func newACLServicer(
	require *require.Assertions,
	revisionFuzzingTimedelta time.Duration,
	gcWindow time.Duration,
) (api.ACLServiceServer, uint64) {
	emptyDS, err := memdb.NewMemdbDatastore(0, revisionFuzzingTimedelta, gcWindow)
	require.NoError(err)

	ds, revision := tf.StandardDatastoreWithData(emptyDS, require)

	dispatch, err := graph.NewLocalDispatcher(ds)
	require.NoError(err)

	return NewACLServer(ds, dispatch, 50), revision
}

func verifyTuples(expected []string, found []*api.RelationTuple, require *require.Assertions) {
	expectedTuples := make(map[string]struct{}, len(expected))
	for _, expTpl := range expected {
		expectedTuples[expTpl] = struct{}{}
	}

	for _, foundTpl := range found {
		serialized := tuple.String(foundTpl)
		require.NotZero(serialized)
		_, ok := expectedTuples[serialized]
		require.True(ok, "unexpected tuple: %s", serialized)
		delete(expectedTuples, serialized)
	}

	require.Empty(expectedTuples, "expected tuples remaining: %#v", expectedTuples)
}