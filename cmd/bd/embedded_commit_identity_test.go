package main

import "testing"

func TestResolvedEmbeddedCommitIdentity(t *testing.T) {
	savedActor, savedSource := actor, actorSource
	t.Cleanup(func() { actor, actorSource = savedActor, savedSource })
	actor, actorSource = "agent-reviewer", "env"
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	name, email := resolvedEmbeddedCommitIdentity()
	if name != "agent-reviewer" || email != "beads@local" {
		t.Fatalf("agent attributed to unrelated human: %q <%s>", name, email)
	}
	t.Setenv("GIT_AUTHOR_NAME", "Alice Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "alice@example.com")
	name, email = resolvedEmbeddedCommitIdentity()
	if name != "Alice Author" || email != "alice@example.com" {
		t.Fatalf("explicit pair lost: %q <%s>", name, email)
	}
}
