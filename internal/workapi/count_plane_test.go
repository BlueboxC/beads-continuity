package workapi

import (
	"github.com/steveyegge/beads/issueops"
	"reflect"
	"testing"
)

func TestCountNamedInfraUsesListPlane(t *testing.T) {
	for _, cfg := range []ListConfig{{CustomTypes: []string{"robot"}}, {CustomTypes: []string{"robot"}, InfraSet: map[string]bool{"robot": true}}} {
		for _, typ := range []string{"", "task", "message", "robot"} {
			for _, include := range []bool{false, true} {
				count, err := BuildCountFilter(issueops.CountRequest{IssueType: typ, IncludeInfra: include}, cfg)
				if err != nil {
					t.Fatal(err)
				}
				list, err := BuildListFilter(issueops.ListRequest{IssueType: typ, IncludeInfra: include, AllFlag: true}, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if count.SkipWisps != list.SkipWisps || !reflect.DeepEqual(count.Ephemeral, list.Ephemeral) {
					t.Errorf("type=%q includeInfra=%v custom=%v count plane=(%v,%v), list=(%v,%v)", typ, include, cfg.InfraSet, count.SkipWisps, count.Ephemeral, list.SkipWisps, list.Ephemeral)
				}
			}
		}
	}
}
