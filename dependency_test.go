package main

import "testing"

// The dependency view heads each group with the dependency container
// itself so groups read as stacks in the shared card grid.
func TestBuildDependencyGroupedHeadsGroupWithRoot(t *testing.T) {
	cards := []containerCard{
		{ContainerID: "db-id", ContainerName: "p-db-1", Name: "db", ComposeProject: "p", ServiceName: "db"},
		{ContainerID: "app-id", ContainerName: "p-app-1", Name: "app", ComposeProject: "p", ServiceName: "app", DependsOn: []string{"db"}},
		{ContainerID: "api-id", ContainerName: "p-api-1", Name: "api", ComposeProject: "p", ServiceName: "api", DependsOn: []string{"db"}},
	}
	grouped, projects := buildDependencyGrouped(cards)
	if len(projects) != 1 || projects[0] != "db" {
		t.Fatalf("projects = %v, want [db]", projects)
	}
	members := grouped["db"]
	if len(members) != 3 {
		t.Fatalf("db group has %d members, want 3 (root + 2 dependents)", len(members))
	}
	if members[0].ContainerID != "db-id" {
		t.Fatalf("db group head = %q, want the db root first", members[0].Name)
	}
	if members[1].Name != "api" || members[2].Name != "app" {
		t.Fatalf("db group dependents = [%q %q], want [api app] sorted", members[1].Name, members[2].Name)
	}
	if _, ok := grouped["Standalone"]; ok {
		t.Fatalf("Standalone should be absent when every container resolves, got %v", grouped["Standalone"])
	}
}

func TestBuildDependencyGroupedStandaloneHoldsOnlyUnresolvable(t *testing.T) {
	cards := []containerCard{
		{ContainerID: "lonely-id", ContainerName: "p-lonely-1", Name: "lonely", ComposeProject: "p", ServiceName: "lonely"},
		{ContainerID: "orphan-id", ContainerName: "p-orphan-1", Name: "orphan", ComposeProject: "p", ServiceName: "orphan", DependsOn: []string{"missing"}},
	}
	grouped, projects := buildDependencyGrouped(cards)
	if len(projects) != 1 || projects[0] != "Standalone" {
		t.Fatalf("projects = %v, want [Standalone]", projects)
	}
	members := grouped["Standalone"]
	if len(members) != 2 || members[0].Name != "lonely" || members[1].Name != "orphan" {
		t.Fatalf("Standalone = %v, want [lonely orphan] sorted", members)
	}
}

func TestBuildDependencyGroupedChainedAndMultiDeps(t *testing.T) {
	cards := []containerCard{
		{ContainerID: "db-id", ContainerName: "p-db-1", Name: "db", ComposeProject: "p", ServiceName: "db"},
		{ContainerID: "cache-id", ContainerName: "p-cache-1", Name: "cache", ComposeProject: "p", ServiceName: "cache"},
		{ContainerID: "mid-id", ContainerName: "p-mid-1", Name: "mid", ComposeProject: "p", ServiceName: "mid", DependsOn: []string{"db"}},
		{ContainerID: "leaf-id", ContainerName: "p-leaf-1", Name: "leaf", ComposeProject: "p", ServiceName: "leaf", DependsOn: []string{"mid"}},
		{ContainerID: "work-id", ContainerName: "p-work-1", Name: "worker", ComposeProject: "p", ServiceName: "worker", DependsOn: []string{"db", "cache"}},
	}
	grouped, _ := buildDependencyGrouped(cards)
	// A chained middle keeps both seats: dependent under db, root of mid.
	if got := names(grouped["db"]); len(got) != 3 || got[0] != "db" || got[1] != "mid" || got[2] != "worker" {
		t.Fatalf("db group = %v, want [db mid worker]", got)
	}
	if got := names(grouped["mid"]); len(got) != 2 || got[0] != "mid" || got[1] != "leaf" {
		t.Fatalf("mid group = %v, want [mid leaf]", got)
	}
	if got := names(grouped["cache"]); len(got) != 2 || got[0] != "cache" || got[1] != "worker" {
		t.Fatalf("cache group = %v, want [cache worker]", got)
	}
}

func TestBuildDependencyGroupedScopesServiceLookupByProject(t *testing.T) {
	cards := []containerCard{
		{ContainerID: "a-db", ContainerName: "a-db-1", Name: "db", ComposeProject: "a", ServiceName: "db"},
		{ContainerID: "b-db", ContainerName: "b-db-1", Name: "db", ComposeProject: "b", ServiceName: "db"},
		{ContainerID: "a-app", ContainerName: "a-app-1", Name: "app", ComposeProject: "a", ServiceName: "app", DependsOn: []string{"db"}},
	}
	grouped, projects := buildDependencyGrouped(cards)
	if len(projects) != 2 {
		t.Fatalf("projects = %v, want [Standalone db]", projects)
	}
	// Both display names are "db", so both projects' dependents land under
	// one "db" key, headed by the first resolved root.
	if len(grouped["db"]) != 2 {
		t.Fatalf("db group has %d members, want 2 (root + app)", len(grouped["db"]))
	}
	if len(grouped["Standalone"]) != 1 || grouped["Standalone"][0].ContainerID != "b-db" {
		t.Fatalf("Standalone = %v, want only the b root nothing depends on", names(grouped["Standalone"]))
	}
}

func names(cards []containerCard) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.Name)
	}
	return out
}
