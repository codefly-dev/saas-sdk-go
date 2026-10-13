package moduleauthority

import (
	"os"
	"path/filepath"
	"testing"
)

// A pod is told nothing and reads its mount; a native `codefly run` names the
// file it projected the token to. Without the variable the path is the pod's,
// so a module under a native run that ignored it would open a path the process
// cannot read and mint nothing.
func TestWorkloadTokenFileFollowsTheVariableAndFallsBackToTheMount(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv(WorkloadTokenFileVariable, "")
		if err := os.Unsetenv(WorkloadTokenFileVariable); err != nil {
			t.Fatal(err)
		}
		if got := WorkloadTokenFile(); got != ProjectedTokenFile(PodWorkloadTokenFile) {
			t.Fatalf("WorkloadTokenFile() = %q, want the pod mount %q", got, PodWorkloadTokenFile)
		}
	})
	t.Run("blank", func(t *testing.T) {
		t.Setenv(WorkloadTokenFileVariable, " \t")
		if got := WorkloadTokenFile(); got != ProjectedTokenFile(PodWorkloadTokenFile) {
			t.Fatalf("WorkloadTokenFile() = %q, want the pod mount %q", got, PodWorkloadTokenFile)
		}
	})
	t.Run("named", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte(" projected-token\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(WorkloadTokenFileVariable, " "+path+" ")
		source := WorkloadTokenFile()
		if source != ProjectedTokenFile(path) {
			t.Fatalf("WorkloadTokenFile() = %q, want the named file %q", source, path)
		}
		token, err := source.ProjectedToken()
		if err != nil || token != "projected-token" {
			t.Fatalf("ProjectedToken() = %q, %v; want the named file's token", token, err)
		}
	})
}

// The two names are contracts with other repositories, not choices made here:
// the CLI exports the variable to a native run (cli cmd/run/native_cluster.go,
// workloadTokenFileVariable) and a cell's render mounts the token at the path.
func TestWorkloadTokenNamesAreTheCodeflyContract(t *testing.T) {
	if WorkloadTokenFileVariable != "CODEFLY__WORKLOAD_TOKEN_FILE" {
		t.Errorf("WorkloadTokenFileVariable = %q", WorkloadTokenFileVariable)
	}
	if PodWorkloadTokenFile != "/var/run/secrets/codefly/token" {
		t.Errorf("PodWorkloadTokenFile = %q", PodWorkloadTokenFile)
	}
}
