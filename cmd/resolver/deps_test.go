package main

import (
	"os/exec"
	"strings"
	"testing"
)

// El plano de datos no habla con la base de datos ni usa dependencias externas
// (ADR 0002 y ADR 0006).
func TestResolverHasNoExternalDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skip("go list no disponible:", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if first := strings.SplitN(dep, "/", 2)[0]; strings.Contains(first, ".") {
			t.Errorf("el resolver importa una dependencia externa: %s", dep)
		}
	}
}
