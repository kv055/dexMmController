package robosats

import "testing"

func TestBase91Encode(t *testing.T) {
	// Reference vector for standard basE91 (Joachim Henke).
	if got := base91Encode([]byte("Hello, World!")); got != ">OwJh>}AQ;r@@Y?F" {
		t.Fatalf("base91Encode = %q, want >OwJh>}AQ;r@@Y?F", got)
	}
	if got := base91Encode(nil); got != "" {
		t.Fatalf("base91Encode(nil) = %q, want empty", got)
	}
}

func TestAuthHeaderToken(t *testing.T) {
	// base91(sha256("robot-token")), the format RoboSats coordinators accept
	// (39-40 character basE91 string).
	want := "X9~[@d4dX\"tB_V^&eKjU0g@>@&^{$eQH`c!NfX{A"
	if got := AuthHeaderToken("robot-token"); got != want {
		t.Fatalf("AuthHeaderToken = %q, want %q", got, want)
	}
}
