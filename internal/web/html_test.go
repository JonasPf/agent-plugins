package web

import "testing"

// What a browser hands back for a body it cannot render as a document: a head
// of nothing but metadata, and the bytes themselves in a single <pre>.
const plainDOM = `<html><head><meta name="color-scheme" content="light dark">` +
	`<meta charset="utf-8"></head><body><pre>{"a": 1 &amp; 2}</pre></body></html>`

func TestUnwrapPlain(t *testing.T) {
	t.Run("returns the body verbatim", func(t *testing.T) {
		got, ok := UnwrapPlain(plainDOM)
		if !ok || got != `{"a": 1 & 2}` {
			t.Errorf("got %q, %v", got, ok)
		}
	})
	t.Run("leaves a real document alone", func(t *testing.T) {
		if _, ok := UnwrapPlain("<html><head><title>T</title></head><body><pre>code</pre></body></html>"); ok {
			t.Error("a document was unwrapped as if it were plain text")
		}
	})
	t.Run("content beside the pre makes it a document", func(t *testing.T) {
		if _, ok := UnwrapPlain(`<html><head><meta charset="utf-8"></head><body><h1>Hi</h1><pre>code</pre></body></html>`); ok {
			t.Error("a document was unwrapped as if it were plain text")
		}
	})
}

// A rendered DOM arrives with its whitespace already squeezed out — often as
// one enormous line — so structure has to come from the tags themselves.
func TestToText(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"block tags become line breaks",
			"<html><body><h1>Title</h1><p>One</p><p>Two</p></body></html>", "Title\nOne\nTwo"},
		{"inline tags keep words apart", "<p>a<b>b</b>c</p>", "a b c"},
		{"scripts and styles go with their contents",
			"<body><script>var x = 1 < 2;</script><style>p{color:red}</style><p>Kept</p></body>", "Kept"},
		{"entities are decoded", "<p>Tom &amp; Jerry &#39;95 &nbsp;ok</p>", "Tom & Jerry '95 ok"},
		{"empty elements leave no blank lines",
			"<p>a</p><div></div><div></div><div></div><p>b</p>", "a\nb"},
		{"a comment is not text", "<p>a</p><!-- hidden --><p>b</p>", "a\nb"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ToText(c.in); got != c.want {
				t.Errorf("ToText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
