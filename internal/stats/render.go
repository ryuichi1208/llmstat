package stats

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func ms(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}

func msInt(d time.Duration) int64 {
	return d.Milliseconds()
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	left := (width - len(s)) / 2
	right := width - len(s) - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func (s *Stats) Render(w io.Writer) {
	dns := dur(s.DNSStart, s.DNSDone)
	tcp := dur(s.ConnectStart, s.ConnectDone)
	tlsH := dur(s.TLSStart, s.TLSDone)
	ttft := dur(s.ReqStart, s.FirstChunk)
	if s.FirstChunk.IsZero() {
		ttft = dur(s.ReqStart, s.GotFirstByte)
	}
	gen := dur(s.FirstChunk, s.LastChunk)
	tail := dur(s.LastChunk, s.EOF)
	total := dur(s.ReqStart, s.EOF)
	if s.EOF.IsZero() {
		total = dur(s.ReqStart, time.Now())
	}

	hasAuth := s.AuthDuration > 0
	colAuth := pad(ms(s.AuthDuration), 8)
	colDNS := pad(ms(dns), 11)
	colTCP := pad(ms(tcp), 16)
	colTLS := pad(ms(tlsH), 15)
	colTTFT := pad(ms(ttft), 21)
	colGen := pad(ms(gen), 13)
	colTail := pad(ms(tail), 7)

	var hdr, bar string
	if hasAuth {
		hdr = fmt.Sprintf("  %s  %s  %s  %s  %s  %s  %s",
			cRed(" Auth   "),
			cCyan("DNS Lookup "),
			cGreen("TCP Connection"),
			cYellow("TLS Handshake"),
			cMagenta("TTFT (Server Think) "),
			cCyan("Generation  "),
			cGreen(" Tail "),
		)
		bar = fmt.Sprintf("[%s|%s|%s|%s|%s|%s|%s]",
			colAuth, colDNS, colTCP, colTLS, colTTFT, colGen, colTail,
		)
	} else {
		hdr = fmt.Sprintf("  %s  %s  %s  %s  %s  %s",
			cCyan("DNS Lookup "),
			cGreen("TCP Connection"),
			cYellow("TLS Handshake"),
			cMagenta("TTFT (Server Think) "),
			cCyan("Generation  "),
			cGreen(" Tail "),
		)
		bar = fmt.Sprintf("[%s|%s|%s|%s|%s|%s]",
			colDNS, colTCP, colTLS, colTTFT, colGen, colTail,
		)
	}

	fmt.Fprintln(w, hdr)
	fmt.Fprintln(w, bar)

	authOffset := s.AuthDuration
	namelookup := authOffset + dns
	connect := namelookup + tcp
	tlsEnd := connect + tlsH
	ttftEnd := tlsEnd + ttft
	genEnd := ttftEnd + gen
	totalT := genEnd + tail
	if totalT == 0 && total > 0 {
		totalT = authOffset + total
	}

	fmt.Fprintln(w)
	if hasAuth {
		fmt.Fprintf(w, "         auth:%s\n", ms(authOffset))
	}
	fmt.Fprintf(w, "  namelookup:%s\n", ms(namelookup))
	fmt.Fprintf(w, "      connect:%s\n", ms(connect))
	if tlsH > 0 {
		fmt.Fprintf(w, "       tls:%s\n", ms(tlsEnd))
	}
	fmt.Fprintf(w, "         ttft:%s\n", ms(ttftEnd))
	fmt.Fprintf(w, "      gen_end:%s\n", ms(genEnd))
	fmt.Fprintf(w, "        total:%s\n", ms(totalT))

	fmt.Fprintln(w)
	fmt.Fprintln(w, cBold("Stream stats:"))
	if s.Provider != "" {
		fmt.Fprintf(w, "  provider:      %s\n", s.Provider)
	}
	if s.InputTokens > 0 || s.TotalTokens > 0 {
		fmt.Fprintf(w, "  input tokens:  %d\n", s.InputTokens)
		fmt.Fprintf(w, "  output tokens: %d\n", s.Tokens)
		if s.ThinkTokens > 0 {
			fmt.Fprintf(w, "  thinking tok:  %d\n", s.ThinkTokens)
		}
		fmt.Fprintf(w, "  total tokens:  %d\n", s.TotalTokens)
	} else {
		fmt.Fprintf(w, "  tokens:        %d\n", s.Tokens)
	}
	fmt.Fprintf(w, "  first chunk:   %s      (TTFT)\n", ms(ttft))

	if itl := ComputeITL(s.ChunkTimes); itl.Count > 0 {
		fmt.Fprintf(w, "  inter-chunk:   p50=%dms  p95=%dms  max=%dms  (n=%d)\n",
			msInt(itl.P50), msInt(itl.P95), msInt(itl.Max), itl.Count)
	} else {
		fmt.Fprintln(w, "  inter-chunk:   n/a")
	}

	if gen > 0 && s.Tokens > 0 {
		fmt.Fprintf(w, "  throughput:    %.1f tok/s\n",
			float64(s.Tokens)/gen.Seconds())
	}
	fmt.Fprintf(w, "  tail:          %s       (last chunk → conn close)\n", ms(tail))
	if s.Model != "" {
		fmt.Fprintf(w, "  model:         %s\n", s.Model)
	}
	if s.Status != 0 {
		fmt.Fprintf(w, "  status:        %d\n", s.Status)
	}
	if s.ErrorMsg != "" {
		fmt.Fprintln(w, cRed("  error:         "+s.ErrorMsg))
	}
}

var useColor = isTerminal(os.Stdout)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func colorize(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func cCyan(s string) string    { return colorize("36", s) }
func cGreen(s string) string   { return colorize("32", s) }
func cYellow(s string) string  { return colorize("33", s) }
func cMagenta(s string) string { return colorize("35", s) }
func cRed(s string) string     { return colorize("31", s) }
func cBold(s string) string    { return colorize("1", s) }
