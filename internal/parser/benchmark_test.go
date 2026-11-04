package parser

import "testing"

func BenchmarkParse(b *testing.B) {
	cases := map[string]struct {
		p    Parser
		line string
	}{
		"generic": {Generic{}, `2026-10-01T12:34:56Z ERROR worker-7 connection reset by peer`},
		"json":    {JSON{}, `{"timestamp":"2026-10-01T12:34:56Z","level":"ERROR","message":"connection reset by peer","service":"gateway","attempt":3}`},
	}
	for name, tc := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.line)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := tc.p.Parse(tc.line); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
