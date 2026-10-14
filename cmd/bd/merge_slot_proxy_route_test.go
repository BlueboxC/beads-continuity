package main

import "testing"

func TestProxyMergeSlotAndSupersedeAdmission(t *testing.T) {
	for _, args := range [][]string{{"merge-slot", "create"}, {"merge-slot", "check"}, {"merge-slot", "acquire"}, {"merge-slot", "release"}, {"supersede"}} {
		cmd, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateProxyRegistryBeforeProvider(cmd, ProxyTopologyManagedLocal); err != nil {
			t.Fatalf("%v refused: %v", args, err)
		}
	}
}
