package semver

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"1.2.3", "1.2.3", false},
		{"v1.2.3", "1.2.3", false},
		{"V2.0.1", "2.0.1", false},
		{"1.2", "1.2.0", false},
		{"3", "3.0.0", false},
		{"1.2.3-rc.1+build.42", "1.2.3-rc.1+build.42", false},
		{"v0.0.0-20240101120000-abcdef123456", "0.0.0-20240101120000-abcdef123456", false},
		{"", "", true},
		{"1.2.3.4", "", true},
		{"1.2.x", "", true},
		{"1.2.3-", "", true},
		{"1.2.3+", "", true},
		{"1.2.3-alpha..1", "", true},
	}

	for _, tc := range tests {
		got, err := Parse(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) expected error, got %v", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("Parse(%q).String() = %q, want %q", tc.input, got.String(), tc.want)
		}
	}
}

func TestParseStrict(t *testing.T) {
	if _, err := ParseStrict("1.2.3"); err != nil {
		t.Fatalf("ParseStrict(1.2.3) unexpected error: %v", err)
	}
	if _, err := ParseStrict("1.2"); err == nil {
		t.Fatalf("ParseStrict(1.2) expected error")
	}
	if _, err := ParseStrict("01.2.3"); err == nil {
		t.Fatalf("ParseStrict(01.2.3) expected error")
	}
}

func TestCompare(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.2.0",
		"2.0.0",
	}

	for i := 0; i < len(ordered); i++ {
		a, err := Parse(ordered[i])
		if err != nil {
			t.Fatalf("Parse(%q): %v", ordered[i], err)
		}
		if Compare(a, a) != 0 {
			t.Errorf("Compare(%s, %s) != 0", ordered[i], ordered[i])
		}
		for j := i + 1; j < len(ordered); j++ {
			b, err := Parse(ordered[j])
			if err != nil {
				t.Fatalf("Parse(%q): %v", ordered[j], err)
			}
			if got := Compare(a, b); got != -1 {
				t.Errorf("Compare(%s, %s) = %d, want -1", ordered[i], ordered[j], got)
			}
			if got := Compare(b, a); got != 1 {
				t.Errorf("Compare(%s, %s) = %d, want 1", ordered[j], ordered[i], got)
			}
		}
	}

	a, _ := Parse("1.0.0+build1")
	b, _ := Parse("1.0.0+build2")
	if Compare(a, b) != 0 {
		t.Errorf("build metadata must be ignored in Compare")
	}
}

func TestIsMajorUpgradeAndInRange(t *testing.T) {
	v100, _ := Parse("1.0.0")
	v150, _ := Parse("1.5.0")
	v200, _ := Parse("2.0.0")
	v210, _ := Parse("2.1.0")

	if IsMajorUpgrade(v100, v150) {
		t.Errorf("1.0.0 -> 1.5.0 should not be a major upgrade")
	}
	if !IsMajorUpgrade(v150, v200) {
		t.Errorf("1.5.0 -> 2.0.0 should be a major upgrade")
	}
	if IsMajorUpgrade(v200, v100) {
		t.Errorf("downgrade should not be a major upgrade")
	}

	if InRange(v100, v100, v200) {
		t.Errorf("fromExclusive should be excluded")
	}
	if !InRange(v150, v100, v200) {
		t.Errorf("1.5.0 should be in (1.0.0, 2.0.0]")
	}
	if !InRange(v200, v100, v200) {
		t.Errorf("2.0.0 should be in (1.0.0, 2.0.0]")
	}
	if InRange(v210, v100, v200) {
		t.Errorf("2.1.0 should not be in (1.0.0, 2.0.0]")
	}
}
