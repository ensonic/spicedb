package developmentmembership

import (
	"fmt"
	"strings"

	"github.com/authzed/spicedb/internal/datasets"
	core "github.com/authzed/spicedb/pkg/proto/core/v1"
	"github.com/authzed/spicedb/pkg/tuple"
)

// TrackingSubjectSet defines a set that tracks accessible subjects and their associated
// relationships.
//
// NOTE: This is designed solely for the developer API and testing and should *not* be used in any
// performance sensitive code.
type TrackingSubjectSet struct {
	setByType map[string]datasets.BaseSubjectSet[FoundSubject]
}

// NewTrackingSubjectSet creates a new TrackingSubjectSet, with optional initial subjects.
func NewTrackingSubjectSet(subjects ...FoundSubject) *TrackingSubjectSet {
	tss := &TrackingSubjectSet{
		setByType: map[string]datasets.BaseSubjectSet[FoundSubject]{},
	}
	for _, subject := range subjects {
		tss.Add(subject)
	}
	return tss
}

// AddFrom adds the subjects found in the other set to this set.
func (tss *TrackingSubjectSet) AddFrom(otherSet *TrackingSubjectSet) {
	for key, oss := range otherSet.setByType {
		tss.getSetForKey(key).UnionWithSet(oss)
	}
}

// RemoveFrom removes any subjects found in the other set from this set.
func (tss *TrackingSubjectSet) RemoveFrom(otherSet *TrackingSubjectSet) {
	for key, oss := range otherSet.setByType {
		tss.getSetForKey(key).SubtractAll(oss)
	}
}

// Add adds the given subjects to this set.
func (tss *TrackingSubjectSet) Add(subjectsAndResources ...FoundSubject) {
	for _, fs := range subjectsAndResources {
		tss.getSet(fs).Add(fs)
	}
}

func keyFor(fs FoundSubject) string {
	return fmt.Sprintf("%s#%s", fs.subject.Namespace, fs.subject.Relation)
}

func (tss *TrackingSubjectSet) getSetForKey(key string) datasets.BaseSubjectSet[FoundSubject] {
	if existing, ok := tss.setByType[key]; ok {
		return existing
	}

	parts := strings.Split(key, "#")

	created := datasets.NewBaseSubjectSet[FoundSubject](
		func(subjectID string, caveatExpression *core.CaveatExpression, excludedSubjects []FoundSubject, sources ...FoundSubject) FoundSubject {
			fs := NewFoundSubject(&core.DirectSubject{
				Subject: &core.ObjectAndRelation{
					Namespace: parts[0],
					ObjectId:  subjectID,
					Relation:  parts[1],
				},
				CaveatExpression: caveatExpression,
			})
			fs.excludedSubjects = excludedSubjects
			fs.caveatExpression = caveatExpression
			for _, source := range sources {
				if source.relationships != nil {
					fs.relationships.UpdateFrom(source.relationships)
				}
			}
			return fs
		},
	)
	tss.setByType[key] = created
	return created
}

func (tss *TrackingSubjectSet) getSet(fs FoundSubject) datasets.BaseSubjectSet[FoundSubject] {
	fsKey := keyFor(fs)
	return tss.getSetForKey(fsKey)
}

// Get returns the found subject in the set, if any.
func (tss *TrackingSubjectSet) Get(subject *core.ObjectAndRelation) (FoundSubject, bool) {
	set, ok := tss.setByType[fmt.Sprintf("%s#%s", subject.Namespace, subject.Relation)]
	if !ok {
		return FoundSubject{}, false
	}

	return set.Get(subject.ObjectId)
}

// Contains returns true if the set contains the given subject.
func (tss *TrackingSubjectSet) Contains(subject *core.ObjectAndRelation) bool {
	_, ok := tss.Get(subject)
	return ok
}

// Exclude returns a new set that contains the items in this set minus those in the other set.
func (tss *TrackingSubjectSet) Exclude(otherSet *TrackingSubjectSet) *TrackingSubjectSet {
	newSet := NewTrackingSubjectSet()

	for key, bss := range tss.setByType {
		cloned := bss.Clone()
		if oss, ok := otherSet.setByType[key]; ok {
			cloned.SubtractAll(oss)
		}

		newSet.setByType[key] = cloned
	}

	return newSet
}

// Intersect returns a new set that contains the items in this set *and* the other set. Note that
// if wildcard is found in *both* sets, it will be returned *along* with any concrete subjects found
// on the other side of the intersection.
func (tss *TrackingSubjectSet) Intersect(otherSet *TrackingSubjectSet) *TrackingSubjectSet {
	newSet := NewTrackingSubjectSet()

	for key, bss := range tss.setByType {
		if oss, ok := otherSet.setByType[key]; ok {
			cloned := bss.Clone()
			cloned.IntersectionDifference(oss)
			newSet.setByType[key] = cloned
		}
	}

	return newSet
}

// ApplyParentCaveatExpression applies the given parent caveat expression (if any) to each subject set.
func (tss *TrackingSubjectSet) ApplyParentCaveatExpression(parentCaveatExpr *core.CaveatExpression) {
	if parentCaveatExpr == nil {
		return
	}

	for key, bss := range tss.setByType {
		tss.setByType[key] = bss.WithParentCaveatExpression(parentCaveatExpr)
	}
}

// removeExact removes the given subject(s) from the set. If the subject is a wildcard, only
// the exact matching wildcard will be removed.
func (tss *TrackingSubjectSet) removeExact(subjects ...*core.ObjectAndRelation) {
	for _, subject := range subjects {
		if set, ok := tss.setByType[fmt.Sprintf("%s#%s", subject.Namespace, subject.Relation)]; ok {
			set.UnsafeRemoveExact(FoundSubject{
				subject: subject,
			})
		}
	}
}

func (tss *TrackingSubjectSet) getSubjects() []string {
	var subjects []string
	for _, subjectSet := range tss.setByType {
		for _, foundSubject := range subjectSet.AsSlice() {
			subjects = append(subjects, tuple.StringONR(foundSubject.subject))
		}
	}
	return subjects
}

// ToSlice returns a slice of all subjects found in the set.
func (tss *TrackingSubjectSet) ToSlice() []FoundSubject {
	subjects := []FoundSubject{}
	for _, bss := range tss.setByType {
		subjects = append(subjects, bss.AsSlice()...)
	}

	return subjects
}

// ToFoundSubjects returns the set as a FoundSubjects struct.
func (tss *TrackingSubjectSet) ToFoundSubjects() FoundSubjects {
	return FoundSubjects{tss}
}

// IsEmpty returns true if the tracking subject set is empty.
func (tss *TrackingSubjectSet) IsEmpty() bool {
	for _, bss := range tss.setByType {
		if !bss.IsEmpty() {
			return false
		}
	}
	return true
}
