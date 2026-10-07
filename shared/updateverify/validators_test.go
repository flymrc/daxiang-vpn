package updateverify

import "testing"

func TestScopeExportCopiesCannotChangeCanonicalValidation(t *testing.T) {
	original := Scope{"cli", "windows", "amd64", "stable"}
	if !ValidScope(original) {
		t.Fatal("canonical scope refused")
	}
	values := ScopeEnums()
	for name, entries := range values {
		entries[0] = "untrusted-override"
		values[name] = append(entries, "untrusted-extra")
	}
	delete(values, "product")
	if !ValidScope(original) || ValidScope(Scope{"untrusted-override", "windows", "amd64", "stable"}) || ScopeEnums()["product"][0] != "cli" {
		t.Fatal("exported enum copy changed trust boundary")
	}
	if _, e := CompareVersions("4294967296.0.0", "1.0.0"); e == nil {
		t.Fatal("version overflow accepted by exported helper")
	}
	if compare, e := CompareVersions("1.0.0-99999999999999999999999", "1.0.0-100000000000000000000000"); e != nil || compare >= 0 {
		t.Fatal("numeric prerelease helper lost precedence")
	}
}
