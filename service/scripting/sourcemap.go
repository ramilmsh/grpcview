package scripting

// sourcemap.go — map a runtime error's line in the ASSEMBLED source back to the author's
// original line, using the source map esbuild emits for a compiled script.
//
// QuickJS reports an uncaught error at its line in the string we actually evaluate, which
// is `inputPrelude + compiled.code`: shifted down by the prelude, and (for the bundler
// path) further shifted by esbuild's banner/helpers and any inlined dependency code. A
// minimal source-map decoder (JSON + base64 VLQ "mappings") turns that generated position
// back into the author's line, so *JSError.Line reads as the line the user wrote.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// smSegment is one decoded mapping: a generated column on some generated line, and the
// original source (index + line + column) it came from. Lines/columns are 0-based, as in
// the source-map spec.
type smSegment struct {
	genCol  int
	srcIdx  int
	srcLine int
	srcCol  int
}

// sourceMap is a decoded v3 source map: the source list plus, per generated line, the
// segments that have an original position (segments with no source are dropped).
type sourceMap struct {
	sources []string
	lines   [][]smSegment
}

// parseSourceMap decodes the JSON envelope and its base64-VLQ "mappings".
func parseSourceMap(raw []byte) (*sourceMap, error) {
	var doc struct {
		Sources  []string `json:"sources"`
		Mappings string   `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	sm := &sourceMap{sources: doc.Sources}

	// srcIdx/srcLine/srcCol are cumulative across the WHOLE mappings string; only genCol
	// resets at the start of each generated line. (Per the source-map v3 spec.)
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
			// A 1-field segment marks a generated position with no original source; it
			// carries no src deltas, so skip it (but genCol still advanced above).
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

// originalPos maps a 0-based generated line/column to its original source path and 0-based
// line. It picks the last segment whose generated column is <= genCol (the mapping in
// effect at that column); with genCol 0 that is the first segment on the line.
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

// b64 is the source-map base64 alphabet (NOT standard base64 order for VLQ, but the same
// character set); index by byte to get the 6-bit digit.
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

// decodeVLQ decodes a base64-VLQ segment into its integer fields. Each value is a
// little-endian sequence of 5-bit groups (bit 5 = continuation); the first group's bit 0
// is the sign.
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
		// Terminal group: bit 0 is the sign, remaining bits are the magnitude.
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
// backtrace (e.g. "    at <script>:12:7" -> 12, 7). "<script>" is the filename JS_Eval is
// given in qjs_wasm.c, so it is the frame that corresponds to the code we assembled.
var stackPosRe = regexp.MustCompile(`<script>:(\d+):(\d+)`)

// remapJSError rewrites je.Line from a line in the assembled source (inputPrelude +
// compiled code) to the author's original line, using the compiled script's source map.
// preludeLines is the number of newlines in the run-time input prelude prepended before the
// code (the eval-side offset). authorPreludeLines is the number of synthetic-prelude lines
// prepended ahead of the body WITHIN the author source before esbuild compiled it (the
// source-side offset, non-zero only for a composed request body — compose.go); it is
// subtracted back out once a position maps INTO the author source, and only then — an inlined
// generator's own frames map to that generator's source and keep their raw line. It is a no-op
// (leaving the raw line) if there is no map, the error predates the code (it landed in the
// input prelude), the position maps inside the synthetic author prelude, or the position does
// not map — never worse than before.
func remapJSError(je *JSError, sourceMap []byte, preludeLines int, authorPreludeLines int) {
	if je == nil || len(sourceMap) == 0 {
		return
	}
	// Prefer the "<script>" frame for an accurate line:column; fall back to the line the
	// generic stack parse already found (with an unknown column).
	line, col := je.Line, 0
	if m := stackPosRe.FindStringSubmatch(je.Stack); m != nil {
		line, _ = strconv.Atoi(m[1])
		col, _ = strconv.Atoi(m[2])
	}
	if line == 0 {
		return
	}

	// Convert the 1-based line in the full string to a 0-based generated line within the
	// code (the first code line sits at full line preludeLines+1).
	genLine := line - preludeLines - 1
	if genLine < 0 {
		return // the error is in the injected prelude, not the author's code
	}

	sm, err := parseSourceMap(sourceMap)
	if err != nil {
		return
	}
	if src, srcLine, ok := sm.originalPos(genLine, col); ok {
		line := srcLine
		// The generator-composition prelude (compose.go) sits in the SAME author source
		// ("script.ts") above the body, so a body position maps to a line shifted down by the
		// prelude length; undo it. ONLY the author source carries this offset — a frame inside
		// an inlined generator maps to that generator's own source and must not be shifted.
		if src == authorSource {
			line -= authorPreludeLines
			if line < 0 {
				return // the position is inside the synthetic prelude — leave the raw line
			}
		}
		je.Line = line + 1 // back to 1-based for humans
	}
}
