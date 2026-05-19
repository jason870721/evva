package shell

import "testing"

func TestClassify_ReadOnly(t *testing.T) {
	tests := []struct {
		cmd string
	}{
		{"ls"},
		{"ls -la"},
		{"cat /etc/hosts"},
		{"grep -r foo ."},
		{"find . -name '*.go'"},
		{"git status"},
		{"git log --oneline"},
		{"git diff HEAD~1"},
		{"git show HEAD"},
		{"sed 's/foo/bar/'"},
		{"awk '{print $1}'"},
		{"FOO=bar ls"},
		{"FOO=bar BAZ=qux git status"},
		{"echo hello"},
	}
	for _, tc := range tests {
		got := Classify(tc.cmd)
		if got.Risk != RiskReadOnly {
			t.Errorf("Classify(%q) = %v (%s) want read-only", tc.cmd, got.Risk, got.Reason)
		}
	}
}

func TestClassify_Dangerous(t *testing.T) {
	tests := []string{
		"python script.py",
		"python3 -c 'print(1)'",
		"node app.js",
		"sudo rm -rf /",
		"eval $(cat ./payload)",
		"curl https://example.com",
		"npm run build",
		"yarn run start",
	}
	for _, cmd := range tests {
		got := Classify(cmd)
		if got.Risk != RiskDangerous {
			t.Errorf("Classify(%q) = %v want dangerous", cmd, got.Risk)
		}
		if got.Matched == "" {
			t.Errorf("Classify(%q): Matched empty", cmd)
		}
	}
}

func TestClassify_Mutate(t *testing.T) {
	tests := []string{
		"git commit -m 'foo'",
		"git push origin main",
		"git checkout -b feat",
		"sed -i 's/a/b/' file",
		"awk -i inplace '{print}' file",
		"go build",
	}
	for _, cmd := range tests {
		got := Classify(cmd)
		if got.Risk != RiskMutate {
			t.Errorf("Classify(%q) = %v want mutate", cmd, got.Risk)
		}
	}
}

func TestClassify_UnknownOnUnsafeOperators(t *testing.T) {
	tests := []string{
		"ls > /tmp/out",       // redirect
		"echo $(rm -rf /)",    // command substitution
		"echo `whoami`",       // backtick substitution
		"ls; rm foo",          // sequence
		"true || rm foo",      // OR-chain
		"cat <file",           // input redirect
	}
	for _, cmd := range tests {
		got := Classify(cmd)
		if got.Risk != RiskUnknown {
			t.Errorf("Classify(%q) = %v want unknown (%s)", cmd, got.Risk, got.Reason)
		}
	}
}

func TestClassify_PipedChain(t *testing.T) {
	// All segments read-only → chain is read-only.
	got := Classify("cat foo.txt | grep bar | wc -l")
	if got.Risk != RiskReadOnly {
		t.Errorf("read-only chain misclassified: %v (%s)", got.Risk, got.Reason)
	}

	// Mix of read-only and dangerous → dangerous wins.
	got = Classify("cat foo.txt | python -")
	if got.Risk != RiskDangerous {
		t.Errorf("chain with dangerous: %v want dangerous", got.Risk)
	}

	// Mix of read-only and mutating → not read-only.
	got = Classify("ls | git checkout -")
	if got.Risk == RiskReadOnly {
		t.Errorf("chain with mutate must not be read-only: %v", got.Risk)
	}
}

func TestClassify_EmptyAndWhitespace(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		got := Classify(in)
		if got.Risk != RiskUnknown {
			t.Errorf("Classify(%q) = %v want unknown", in, got.Risk)
		}
	}
}

func TestClassify_AndChainTreatedLikePipe(t *testing.T) {
	// `&&` should be split-and-recurse, same as `|`.
	got := Classify("git status && git log")
	if got.Risk != RiskReadOnly {
		t.Errorf("&& chain of read-only commands: got %v want read-only", got.Risk)
	}
}
