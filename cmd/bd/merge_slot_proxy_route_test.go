package main

import "testing"

func TestProxyImplementedWorkflowAdmission(t *testing.T) {
	for _, args := range [][]string{{"merge-slot", "create"}, {"merge-slot", "check"}, {"merge-slot", "acquire"}, {"merge-slot", "release"}, {"supersede"}, {"ship"}, {"duplicate"}} {
		cmd, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		for _, topology := range []ProxyTopology{ProxyTopologyManagedLocal, ProxyTopologyExternalTCP, ProxyTopologyTeamServer} {
			if err := validateProxyRegistryBeforeProvider(cmd, topology); err != nil {
				t.Fatalf("%v on %v refused: %v", args, topology, err)
			}
		}
	}
}
