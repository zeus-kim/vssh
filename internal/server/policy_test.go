package server

import "testing"

func mkPolicy(t *testing.T, allow, deny, danger []string) *Policy {
	t.Helper()
	p := &Policy{ExecAllow: allow, ExecDeny: deny, DangerPreapproved: danger}
	var err error
	if p.execDenyRE, err = compileAnchoredWarn("t", "exec_deny", deny); err != nil {
		t.Fatal(err)
	}
	if p.execAllowRE, err = compileAnchoredWarn("t", "exec_allow", allow); err != nil {
		t.Fatal(err)
	}
	if p.dangerRE, err = compileAnchoredWarn("t", "danger_preapproved", danger); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvalExec(t *testing.T) {
	p := mkPolicy(t,
		[]string{`^/usr/bin/rsync -a /src/ vault:$`, `^/bin/echo [a-z]+$`},
		[]string{`^/bin/echo secret$`},
		[]string{`^/usr/bin/systemctl restart vsshd$`},
	)
	cases := []struct {
		cmd         string
		wantAllowed bool
		wantPreapp  bool
	}{
		{`/usr/bin/rsync -a /src/ vault:`, true, false},            // allow
		{`/bin/echo hello`, true, false},                           // allow
		{`/bin/echo secret`, false, false},                         // deny wins (also matches nothing in allow anyway)
		{`/usr/bin/systemctl restart vsshd`, true, true},           // danger_preapproved
		{`/bin/rm -rf /`, false, false},                            // no match -> refuse
		{`/usr/bin/rsync -a /src/ vault:; rm -rf /`, false, false}, // metachar smuggle blocked by anchoring
		{`/bin/echo hello && cat /etc/shadow`, false, false},       // append blocked by anchoring
		{"/bin/echo hello\nrm -rf /", false, false},                // newline injection blocked
	}
	for _, c := range cases {
		allowed, rule, pre := p.EvalExec(c.cmd)
		if allowed != c.wantAllowed || pre != c.wantPreapp {
			t.Errorf("EvalExec(%q) = (allowed=%v pre=%v rule=%s), want allowed=%v pre=%v", c.cmd, allowed, pre, rule, c.wantAllowed, c.wantPreapp)
		}
		if !allowed && rule == "" {
			t.Errorf("EvalExec(%q): denied but empty ruleID", c.cmd)
		}
	}
}

func TestEvalExecDenyBeatsAllow(t *testing.T) {
	// A command matching BOTH an allow and a deny rule must be denied.
	p := mkPolicy(t, []string{`^/bin/echo .*$`}, []string{`^/bin/echo secret$`}, nil)
	if allowed, rule, _ := p.EvalExec("/bin/echo secret"); allowed {
		t.Fatalf("deny must beat allow; got allowed (rule %s)", rule)
	}
}

func TestLoadPolicyMissingFailsClosed(t *testing.T) {
	if _, err := LoadPolicy("definitely-no-such-policy-xyz"); err == nil {
		t.Fatal("missing policy must return error (caller fails closed)")
	}
	if _, err := LoadPolicy("../etc/passwd"); err == nil {
		t.Fatal("path-traversal policy name must be rejected")
	}
}

func TestPathAllowed(t *testing.T) {
	p := &Policy{FilePaths: []string{"/var/backups/**", "/etc/vssh/authorized_keys"}}
	ok := []string{"/var/backups/db.sql", "/var/backups/sub/dir/x", "/var/backups", "/etc/vssh/authorized_keys"}
	bad := []string{"/etc/passwd", "/var/backupsX/y", "/var/backups/../etc/passwd"}
	for _, q := range ok {
		if !p.PathAllowed(q) {
			t.Errorf("PathAllowed(%q) = false, want true", q)
		}
	}
	for _, q := range bad {
		if p.PathAllowed(q) {
			t.Errorf("PathAllowed(%q) = true, want false (escape/out-of-scope)", q)
		}
	}
	// Empty file_paths = deny all (fail-closed).
	if (&Policy{}).PathAllowed("/var/backups/x") {
		t.Error("empty file_paths must deny")
	}
}

func TestFwdTargetMatch(t *testing.T) {
	rules := []string{"10.0.0.0/8:5432", "db.internal:6379", "192.168.1.5:*"}
	ok := [][2]interface{}{{"10.1.2.3", 5432}, {"db.internal", 6379}, {"192.168.1.5", 22}, {"192.168.1.5", 9999}}
	bad := [][2]interface{}{{"10.1.2.3", 5433}, {"db.internal", 5432}, {"8.8.8.8", 5432}, {"192.168.1.6", 22}}
	for _, c := range ok {
		if !fwdTargetMatch(rules, c[0].(string), c[1].(int)) {
			t.Errorf("fwdTargetMatch(%v:%v) = false, want true", c[0], c[1])
		}
	}
	for _, c := range bad {
		if fwdTargetMatch(rules, c[0].(string), c[1].(int)) {
			t.Errorf("fwdTargetMatch(%v:%v) = true, want false", c[0], c[1])
		}
	}
	if fwdTargetMatch(nil, "10.0.0.1", 5432) {
		t.Error("empty rules must deny")
	}
}

func TestRateExceeded(t *testing.T) {
	k := "ratekey-" + t.Name()
	for i := 0; i < 3; i++ {
		if rateExceeded(k, 3) {
			t.Fatalf("attempt %d should be within limit", i+1)
		}
	}
	if !rateExceeded(k, 3) {
		t.Error("4th attempt should exceed limit of 3/min")
	}
	if rateExceeded("other"+t.Name(), 3) {
		t.Error("a different key must have its own window")
	}
	if rateExceeded("x", 0) {
		t.Error("perMin<=0 disables the limit")
	}
}
