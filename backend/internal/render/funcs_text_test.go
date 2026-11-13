package render

import "testing"

func TestExcerpt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    any
		n    any
		want string
	}{
		{"empty", "", 10, ""},
		{"nil", nil, 10, ""},
		{"n zero", "hello", 0, ""},
		{"n negative", "hello", -3, ""},
		{"short", "hello world", 20, "hello world"},
		{"exactly n", "hello world", 11, "hello world"},
		{"cut at word", "hello big world", 12, "hello big..."},
		{"cut on space", "hello world again", 11, "hello world..."},
		{"long first word", "abcdefghij tail", 4, "abcd..."},
		{"paragraphs not glued", "<p>a</p><p>b</p>", 10, "a b"},
		{"entities", "fish &amp; chips&nbsp;&lt;3", 40, "fish & chips <3"},
		{"script and style dropped", "x<script>alert(1)</script><style>p{}</style>y", 10, "x y"},
		{"invalid utf8", "ab\xffcd", 10, "abcd"},
		{"runes", "éééé éééé", 6, "éééé..."},
		{"string n", "hello big world", "12", "hello big..."},
	}
	for _, tc := range tests {
		if got := excerptFn(tc.s, tc.n); got != tc.want {
			t.Errorf("%s: excerpt(%q, %v) = %q, want %q", tc.name, tc.s, tc.n, got, tc.want)
		}
	}
}

func TestInitial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   any
		want string
	}{
		{"jane doe", "J"}, {"  3x", "X"}, {"", ""}, {nil, ""}, {"123 -", ""},
		{"élan", "É"}, {"\xff\xfeq", "Q"}, {42, ""},
	}
	for _, tc := range tests {
		if got := initialFn(tc.in); got != tc.want {
			t.Errorf("initial(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsBlankHTML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   any
		want bool
	}{
		{"", true}, {nil, true}, {"  \n", true}, {"<p></p>", true},
		{"<p>&nbsp; </p>", true}, {"<p>\u00a0</p>", true}, {"<p><br></p>", true},
		{"<script>x()</script>", true}, {"<style>p{}</style>", true},
		{"<p>a</p>", false}, {"plain", false}, {`<p><img src="/a.png"></p>`, false},
		{`<iframe src="/x"></iframe>`, false}, {"<svg></svg>", false},
		{"<p><", false}, {struct{}{}, true},
	}
	for _, tc := range tests {
		if got := isBlankHTML(tc.in); got != tc.want {
			t.Errorf("isBlankHTML(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestVideoID(t *testing.T) {
	t.Parallel()
	const id = "abc123XYZ_-"
	tests := []struct {
		in   any
		want string
	}{
		{"https://www.youtube.com/watch?v=" + id, id},
		{"https://youtube.com/watch?v=" + id + "&t=5", id},
		{"https://m.youtube.com/watch?v=" + id, id},
		{"https://youtu.be/" + id, id},
		{"https://youtu.be/" + id + "?t=1", id},
		{"https://www.youtube.com/embed/" + id, id},
		{"https://www.youtube-nocookie.com/embed/" + id, id},
		{"https://www.youtube.com/shorts/" + id, id},
		{"https://www.youtube.com/live/" + id, id},
		{"https://vimeo.com/123456789", "123456789"},
		{"https://player.vimeo.com/video/123456789?h=x", "123456789"},
		{"https://vimeo.com/abc", ""},
		{"https://www.youtube.com/watch?v=short", ""},
		{"https://www.youtube.com/watch", ""},
		{"ftp://youtu.be/" + id, ""},
		{"javascript:alert(1)", ""},
		{"https://example.test/watch?v=" + id, ""},
		{"https://evil.test/youtu.be/" + id, ""},
		{"", ""}, {nil, ""}, {"%zz", ""}, {7, ""},
	}
	for _, tc := range tests {
		if got := videoID(tc.in); got != tc.want {
			t.Errorf("videoID(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPercent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b any
		want int
	}{
		{3, 4, 75}, {1, 3, 33}, {2, 3, 67}, {"x", 0, 0}, {5, 0, 0}, {0, 5, 0},
		{1, 2, 50}, {1, 200, 1}, {1, 400, 0}, {-1, 2, -50}, {-1, 3, -33},
		{-2, 3, -67}, {1, -3, -33}, {-1, -3, 33}, {nil, nil, 0}, {"3", "4", 75},
		{4, 4, 100}, {8, 4, 200},
	}
	for _, tc := range tests {
		if got := percent(tc.a, tc.b); got != tc.want {
			t.Errorf("percent(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
