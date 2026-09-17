package shell

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckQualification(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		d      Dialect
		status ParseStatus
		offset int
	}{
		{"simple", "cat a && echo b > c", DialectBash, StatusParsed, 0},
		{"heredoc quoted", "python - <<'PY'\nprint(1)\nPY\n", DialectBash, StatusParsed, 0},
		{"incomplete", "if true; then echo x", DialectBash, StatusPartial, 0},
		{"incomplete quote", "echo 'abc", DialectBash, StatusPartial, 5},
		{"invalid", "echo )", DialectBash, StatusInvalid, 5},
		{"posix rejects bash arrays", "a=(1 2)", DialectPOSIX, StatusInvalid, 2},
		{"crlf", "echo a\r\necho b\r\n", DialectBash, StatusParsed, 0},
		{"non utf8 rejected upstream", "echo \xff\xfe", DialectBash, StatusInvalid, 5},
		{"unsupported dialect", "echo", Dialect("fish"), StatusUnsupported, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := Check([]byte(c.src), c.d)
			if st != c.status {
				t.Fatalf("status %s, want %s (err %v)", st, c.status, err)
			}
			if c.offset != 0 {
				var se *SyntaxError
				if !errors.As(err, &se) || se.Offset != c.offset {
					t.Fatalf("offset: got %v, want %d", err, c.offset)
				}
			}
		})
	}
}

func BenchmarkParse1KiB(b *testing.B) {
	src := []byte(strings.Repeat("cd service && go test ./... 2>&1 | tee out.log\n", 22))
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parse(src, DialectBash); err != nil {
			b.Fatal(err)
		}
	}
}
