package tools

import "testing"

// Regresion: una ruta relativa con "/" no debe leerse como absoluta.
// El guardia rechazaba "python3 tmp/x.py" porque el regex tomaba "/x.py".
func TestWorkspaceGuardAceptaRutasRelativas(t *testing.T) {
	dir := t.TempDir()
	tool, err := NewExecTool(dir, true)
	if err != nil {
		t.Fatal(err)
	}

	casos := []struct {
		cmd     string
		bloquea bool
	}{
		{"python3 tmp/check_credit.py", false},
		{"cat data/informe.csv", false},
		{"ls -la", false},
		{`python3 -c "print('hi')"`, false},
		{"cat /etc/passwd", true},
		{"cp a.txt /root/b.txt", true},
		{"echo hola > /dev/null", false},
	}
	for _, c := range casos {
		motivo := tool.guardCommand(c.cmd, dir)
		if (motivo != "") != c.bloquea {
			t.Errorf("%q: bloqueado=%v (esperado %v) motivo=%q",
				c.cmd, motivo != "", c.bloquea, motivo)
		}
	}
}
