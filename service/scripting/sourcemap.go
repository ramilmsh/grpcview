package scripting

// Maps a runtime error's line in the assembled source back to the author's original line,
// via the source map esbuild emits for a compiled script.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// smSegment is one decoded mapping. Lines and columns are 0-based, per the source-map spec.
type smSegment struct {
	genCol  int
	srcIdx  int
	srcLine int
	srcCol  int
}

type sourceMap struct {
	sources []string
	lines   [][]smSegment
}

func parseSourceMap(raw []byte) (*sourceMap, error) {
	var doc struct {
		Sources  []string `json:"sources"`
		Mappings string   `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	sm := &sourceMap{sources: doc.Sources}

	// Per the spec these are cumulative across the WHOLE mappings string; only genCol resets.
	var srcIdx, srcLine, srcCol int
	for _, lineStr := range strings.Split(doc.Mappings, ";") {
		var segs []smSegment
		genCol := 0
		for _, segStr := range strings.Split(lineStr, ",") {
			if segStr == "" {
				continue
			}
			fields, err := decodeVLQ(segStr)
			if err != nil {
				return nil, err
			}
			if len(fields) == 0 {
				continue
			}
			genCol += fields[0]
			// A 1-field segment carries no original position, only the genCol advance above.
			if len(fields) >= 4 {
				srcIdx += fields[1]
				srcLine += fields[2]
				srcCol += fields[3]
				segs = append(segs, smSegment{genCol: genCol, srcIdx: srcIdx, srcLine: srcLine, srcCol: srcCol})
			}
		}
		sm.lines = append(sm.lines, segs)
	}
	return sm, nil
}

func (m *sourceMap) originalPos(genLine, genCol int) (src string, srcLine int, ok bool) {
	if genLine < 0 || genLine >= len(m.lines) {
		return "", 0, false
	}
	segs := m.lines[genLine]
	if len(segs) == 0 {
		return "", 0, false
	}
	chosen := segs[0]
	for _, s := range segs {
		if s.genCol <= genCol {
			chosen = s
		} else {
			break
		}
	}
	if chosen.srcIdx >= 0 && chosen.srcIdx < len(m.sources) {
		src = m.sources[chosen.srcIdx]
	}
	return src, chosen.srcLine, true
}

// b64 is the source-map base64 alphabet, indexed by byte to get the 6-bit digit.
const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var b64idx = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(b64); i++ {
		t[b64[i]] = int8(i)
	}
	return t
}()

// Base64-VLQ: little-endian 5-bit groups, bit 5 = continuation, the first group's bit 0 = sign.
func decodeVLQ(s string) ([]int, error) {
	var out []int
	value, shift := 0, 0
	for i := 0; i < len(s); i++ {
		d := b64idx[s[i]]
		if d < 0 {
			return nil, fmt.Errorf("scripting: bad VLQ char %q", s[i])
		}
		digit := int(d)
		value += (digit & 31) << shift
		if digit&32 != 0 {
			shift += 5
			continue
		}
		neg := value&1 != 0
		value >>= 1
		if neg {
			value = -value
		}
		out = append(out, value)
		value, shift = 0, 0
	}
	return out, nil
}

// stackPosRe pulls the generated line:column out of the "<script>" frame of a QuickJS
// backtrace; "<script>" is the filename JS_Eval is given in qjs_wasm.c.
var stackPosRe = regexp.MustCompile(`<script>:(\d+):(\d+)`)

// remapJSError rewrites je.Line to the author's original line, leaving the raw line whenever
// the position does not map. preludeLines is the eval-side offset (the run-time input
// prelude); authorPreludeLines the synthetic prelude inside the author source (compose.go).
func remapJSError(je *JSError, sourceMap []byte, preludeLines int, authorPreludeLines int) {
	if je == nil || len(sourceMap) == 0 {
		return
	}
	line, col := je.Line, 0
	if m := stackPosRe.FindStringSubmatch(je.Stack); m != nil {
		line, _ = strconv.Atoi(m[1])
		col, _ = strconv.Atoi(m[2])
	}
	if line == 0 {
		return
	}

	genLine := line - preludeLines - 1
	if genLine < 0 {
		return
	}

	sm, err := parseSourceMap(sourceMap)
	if err != nil {
		return
	}
	if src, srcLine, ok := sm.originalPos(genLine, col); ok {
		line := srcLine
		// Only the author source carries that offset; an inlined generator keeps its own lines.
		if src == authorSource {
			line -= authorPreludeLines
			if line < 0 {
				return
			}
		}
		je.Line = line + 1
	}
}
