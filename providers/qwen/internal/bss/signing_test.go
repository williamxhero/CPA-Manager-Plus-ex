package bss

import "testing"

func TestPercentEncodeUnreserved(t *testing.T) {
	input := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	if got := PercentEncode(input); got != input {
		t.Fatalf("got %s", got)
	}
}

func TestPercentEncodeSpecialBytes(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"asterisk", "*", "%2A"}, {"tilde", "~", "~"}, {"space", " ", "%20"},
		{"slash", "/", "%2F"}, {"CJK", "中", "%E4%B8%AD"},
		{"plus", "+", "%2B"}, {"percent", "%20", "%2520"}, {"nul", "\x00", "%00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PercentEncode(tc.input); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestCanonicalQueryRawKeySorting(t *testing.T) {
	params := map[string]string{"中": "c", "a": "b", "Z": "z", "*": "x", "~": "t"}
	want := "%2A=x&Z=z&a=b&~=t&%E4%B8%AD=c"
	if got := CanonicalQuery(params); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCanonicalQueryRawValues(t *testing.T) {
	params := map[string]string{"a b": "*~ /中", "Signature": "ignored"}
	if got := CanonicalQuery(params); got != "a%20b=%2A~%20%2F%E4%B8%AD" {
		t.Fatal(got)
	}
	if params["Signature"] != "ignored" {
		t.Fatal("mutated input")
	}
}

func TestStringToSignDoubleEncoding(t *testing.T) {
	got := StringToSign(map[string]string{"q": "/ a"})
	if got != "GET&%2F&q%3D%252F%2520a" {
		t.Fatal(got)
	}
}

func TestSignatureIndependentFrozenVector(t *testing.T) {
	// Synthetic fixture, not an access credential. Independently derived with
	// Python's urllib.parse.quote(safe='-_.~') on sorted raw keys, followed by
	// hmac.new(b'unit-test-only&', string_to_sign.encode(), hashlib.sha1),
	// then base64.b64encode(digest). The frozen string also detects double encoding.
	params := map[string]string{"Action": "FixtureAction", "Z": "*~ /中", "a": "a+b%", "Format": "JSON"}
	wantString := "GET&%2F&Action%3DFixtureAction%26Format%3DJSON%26Z%3D%252A~%2520%252F%25E4%25B8%25AD%26a%3Da%252Bb%2525"
	if got := StringToSign(params); got != wantString {
		t.Fatal(got)
	}
	if got := Sign(params, "unit-test-only"); got != "nMUtQ6QeyU/kqLvao3MljCQtBwE=" {
		t.Fatal(got)
	}
}

func TestEmptyCanonicalQuery(t *testing.T) {
	if CanonicalQuery(nil) != "" || StringToSign(nil) != "GET&%2F&" {
		t.Fatal("bad empty query")
	}
}
