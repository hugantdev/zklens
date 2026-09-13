package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/hugantdev/zklens/internal/zk"
)

func TestFormatDataEmpty(t *testing.T) {
	got := formatData(nil)
	if !strings.Contains(got, "no data") {
		t.Fatalf("formatData(nil) = %q, want it to say there is no data", got)
	}

	got = formatData([]byte{})
	if !strings.Contains(got, "no data") {
		t.Fatalf("formatData([]byte{}) = %q, want it to say there is no data", got)
	}
}

func TestFormatDataPlainTextShowsRawOnlyNotAsJSON(t *testing.T) {
	got := formatData([]byte("hello world, not json"))

	if !strings.Contains(got, "Raw:") {
		t.Fatalf("formatData(plain text) = %q, want a Raw: section", got)
	}
	if !strings.Contains(got, "hello world, not json") {
		t.Fatalf("formatData(plain text) = %q, want the literal text included", got)
	}
	if strings.Contains(got, "Decoded") {
		t.Fatalf("formatData(plain text) = %q, want no decoded/JSON section for non-JSON text", got)
	}
}

func TestFormatDataValidJSONShowsRawAndDecodedClearlyDelimited(t *testing.T) {
	got := formatData([]byte(`{"a":1,"b":"two"}`))

	if !strings.Contains(got, "Raw:") {
		t.Fatalf("formatData(json) = %q, want a Raw: section", got)
	}
	if !strings.Contains(got, `{"a":1,"b":"two"}`) {
		t.Fatalf("formatData(json) = %q, want the raw literal bytes included verbatim", got)
	}
	if !strings.Contains(got, "Decoded") {
		t.Fatalf("formatData(json) = %q, want a decoded/JSON section", got)
	}

	rawIdx := strings.Index(got, "Raw:")
	decodedIdx := strings.Index(got, "Decoded")
	if rawIdx < 0 || decodedIdx < 0 || rawIdx >= decodedIdx {
		t.Fatalf("formatData(json): Raw section must come before the decoded section, got = %q", got)
	}

	// The decoded section must be pretty-printed (indented), not just a
	// second copy of the compact raw bytes.
	decodedSection := got[decodedIdx:]
	if !strings.Contains(decodedSection, "\n  \"a\"") && !strings.Contains(decodedSection, "\n  \"b\"") {
		t.Fatalf("formatData(json) decoded section = %q, want indented/pretty-printed JSON", decodedSection)
	}
}

func TestFormatDataInvalidUTF8ShowsHexDumpNotRawBytes(t *testing.T) {
	binary := []byte{0x00, 0x01, 0xFF, 0xFE, 'h', 'i', 0x80}
	got := formatData(binary)

	if !strings.Contains(strings.ToLower(got), "hexdump") && !strings.Contains(strings.ToLower(got), "binary") {
		t.Fatalf("formatData(binary) = %q, want it to be identified as binary/hexdump", got)
	}
	if strings.Contains(got, "Raw:") {
		t.Fatalf("formatData(binary) = %q, want it to avoid dumping raw non-UTF8 bytes directly (only the hex form)", got)
	}
	// The hex dump must actually reflect the bytes given.
	if !strings.Contains(got, "ff") && !strings.Contains(got, "FF") {
		t.Fatalf("formatData(binary) = %q, want the 0xFF byte to show up in the hex dump", got)
	}
}

func TestFormatDataControlSequencesShowByteAccurateHexDump(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		hex     string
	}{
		{
			name:    "ESC save cursor",
			payload: []byte("before\x1b7after"),
			hex:     "1b 37",
		},
		{
			name:    "CSI erase display",
			payload: []byte("before\x1b[2Jafter"),
			hex:     "1b 5b 32 4a",
		},
		{
			name:    "OSC title",
			payload: []byte("before\x1b]0;owned\x07after"),
			hex:     "1b 5d 30 3b 6f 77 6e 65 64 07",
		},
		{
			name:    "C0 controls",
			payload: []byte("before\x00\x07\t\rafter"),
			hex:     "00 07 09 0d",
		},
		{
			name:    "C1 CSI",
			payload: []byte("before\xc2\x9b2Jafter"),
			hex:     "c2 9b 32 4a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatData(tt.payload)

			if !strings.Contains(strings.ToLower(got), "hexdump") {
				t.Fatalf("formatData(%q) = %q, want a hexdump", tt.payload, got)
			}
			if strings.Contains(got, string(tt.payload)) {
				t.Fatalf("formatData(%q) = %q, must not render the control payload directly", tt.payload, got)
			}
			compactDump := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(got), " ", ""), "\n", "")
			wantHex := strings.ReplaceAll(tt.hex, " ", "")
			if !strings.Contains(compactDump, wantHex) {
				t.Fatalf("formatData(%q) = %q, want byte-accurate hex %q", tt.payload, got, tt.hex)
			}
		})
	}
}

func TestFormatDataMultilineTextRemainsReadable(t *testing.T) {
	payload := []byte("first line\nsecond line")
	got := formatData(payload)

	if !strings.Contains(got, string(payload)) {
		t.Fatalf("formatData(%q) = %q, want normal multiline text rendered directly", payload, got)
	}
	if strings.Contains(strings.ToLower(got), "hexdump") {
		t.Fatalf("formatData(%q) = %q, do not hex dump normal multiline text", payload, got)
	}
}

func TestHexDumpFormatsOffsetHexAndASCII(t *testing.T) {
	data := []byte("Hi!\x00\x01")
	got := hexDump(data)

	if !strings.HasPrefix(got, "00000000") {
		t.Fatalf("hexDump() = %q, want it to start with an 8-hex-digit offset", got)
	}
	if !strings.Contains(got, "48 69 21") { // 'H'=0x48 'i'=0x69 '!'=0x21
		t.Fatalf("hexDump() = %q, want the hex bytes for 'H','i','!'", got)
	}
	if !strings.Contains(got, "|Hi!..|") {
		t.Fatalf("hexDump() = %q, want the ASCII column to show printable chars and '.' for non-printable bytes", got)
	}
}

func TestHexDumpMultipleLines(t *testing.T) {
	data := make([]byte, 20)
	for i := range data {
		data[i] = byte(i)
	}
	got := hexDump(data)

	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("hexDump(20 bytes) produced %d lines, want 2 (16 bytes/line)", len(lines))
	}
	if !strings.HasPrefix(lines[1], "00000010") {
		t.Fatalf("hexDump() second line = %q, want it to start at offset 00000010", lines[1])
	}
}

func TestFormatStatNilShowsPlaceholderWithoutPanic(t *testing.T) {
	got := formatStat(nil)
	if !strings.Contains(got, "no stat") {
		t.Fatalf("formatStat(nil) = %q, want a placeholder", got)
	}
}

func TestFormatStatIncludesAllFieldsInRequiredOrder(t *testing.T) {
	s := &zk.Stat{
		Czxid:          1,
		Mzxid:          2,
		Ctime:          1700000000000,
		Mtime:          1700000001000,
		Version:        3,
		Cversion:       4,
		Aversion:       5,
		EphemeralOwner: 123456789,
		DataLength:     42,
		NumChildren:    7,
		Pzxid:          8,
	}
	got := formatStat(s)

	order := []string{
		"czxid", "mzxid", "ctime", "mtime", "version", "cversion",
		"aversion", "ephemeralOwner", "dataLength", "numChildren", "pzxid",
	}
	lastIdx := -1
	for _, label := range order {
		idx := strings.Index(got, label)
		if idx < 0 {
			t.Fatalf("formatStat() missing field %q; full output:\n%s", label, got)
		}
		if idx <= lastIdx {
			t.Fatalf("formatStat() field %q out of order; full output:\n%s", label, got)
		}
		lastIdx = idx
	}

	for _, want := range []string{"1", "2", "3", "4", "5", "123456789", "42", "7", "8"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatStat() = %q, want it to contain value %q", got, want)
		}
	}
}

func TestFormatStatTimesIncludeHumanReadableForm(t *testing.T) {
	s := &zk.Stat{Ctime: 1700000000000, Mtime: 1700000000000}
	got := formatStat(s)

	if !strings.Contains(got, "1700000000000") {
		t.Fatalf("formatStat() = %q, want the raw epoch-ms value present", got)
	}
	// 1700000000000 ms == 2023-11-14T22:13:20Z
	if !strings.Contains(got, "2023-11-14") {
		t.Fatalf("formatStat() = %q, want a human-readable RFC3339-ish date derived from ctime/mtime", got)
	}
}

func TestRenderDetailLoadingState(t *testing.T) {
	got := renderDetail(detailState{path: "/foo", loading: true})
	if !strings.Contains(got, "/foo") {
		t.Fatalf("renderDetail(loading) = %q, want the path shown as a title", got)
	}
	if !strings.Contains(got, "loading") {
		t.Fatalf("renderDetail(loading) = %q, want a loading indicator", got)
	}
	if strings.Contains(got, "Stat") {
		t.Fatalf("renderDetail(loading) = %q, want no Stat section while still loading", got)
	}
}

func TestRenderDetailErrorStateHidesDataAndStat(t *testing.T) {
	got := renderDetail(detailState{path: "/bad", err: errors.New("zk: get /bad: zk: node does not exist")})
	if !strings.Contains(got, "node does not exist") {
		t.Fatalf("renderDetail(error) = %q, want the error message shown", got)
	}
	if strings.Contains(got, "Data") || strings.Contains(got, "Stat") {
		t.Fatalf("renderDetail(error) = %q, want no Data/Stat sections when there was an error", got)
	}
}

func TestRenderDetailSuccessShowsDataBeforeStat(t *testing.T) {
	got := renderDetail(detailState{
		path: "/ok",
		data: []byte("hello"),
		stat: &zk.Stat{Version: 1},
	})

	dataIdx := strings.Index(got, "Data")
	statIdx := strings.Index(got, "Stat")
	if dataIdx < 0 || statIdx < 0 || dataIdx >= statIdx {
		t.Fatalf("renderDetail(success) = %q, want a Data section followed by a Stat section", got)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("renderDetail(success) = %q, want the raw data included", got)
	}
	if !strings.Contains(got, "version") {
		t.Fatalf("renderDetail(success) = %q, want the Stat fields included", got)
	}
}

func TestRenderDetailShowsWatchMarkerOnlyWhenWatching(t *testing.T) {
	watching := renderDetail(detailState{path: "/n", data: []byte("x"), stat: &zk.Stat{}, watching: true})
	if !strings.Contains(watching, "◆") {
		t.Fatalf("renderDetail(watching=true) = %q, want the watch marker ◆", watching)
	}

	notWatching := renderDetail(detailState{path: "/n", data: []byte("x"), stat: &zk.Stat{}, watching: false})
	if strings.Contains(notWatching, "◆") {
		t.Fatalf("renderDetail(watching=false) = %q, want no watch marker", notWatching)
	}
}

// --- per-panel renderers ----------------------------------------------------

// The Data and Stat panes are on screen from startup, so they need a real
// empty state: nothing is fetched until the user presses tab on a node.
func TestRenderPanelsShowAPlaceholderBeforeAnythingIsLoaded(t *testing.T) {
	if got := renderDataPanel(detailState{}); !strings.Contains(got, "tab") {
		t.Errorf("renderDataPanel(zero) = %q, want a hint that tab loads a node", got)
	}
	if got := renderStatPanel(detailState{}); got == "" {
		t.Error("renderStatPanel(zero) = \"\", want a placeholder rather than a blank pane")
	}
}

func TestRenderPanelsSplitDataAndStat(t *testing.T) {
	d := detailState{
		path: "/n",
		data: []byte(`{"clave":"valor"}`),
		stat: &zk.Stat{Czxid: 42, Version: 3},
	}

	data := renderDataPanel(d)
	if !strings.Contains(data, `{"clave":"valor"}`) {
		t.Errorf("renderDataPanel = %q, want the raw data", data)
	}
	if strings.Contains(data, "czxid") {
		t.Errorf("renderDataPanel = %q, want no Stat fields — they belong to the Stat pane", data)
	}

	stat := renderStatPanel(d)
	if !strings.Contains(stat, "czxid") || !strings.Contains(stat, "42") {
		t.Errorf("renderStatPanel = %q, want the Stat fields", stat)
	}
	if strings.Contains(stat, "clave") {
		t.Errorf("renderStatPanel = %q, want no data — it belongs to the Data pane", stat)
	}
}

// Both panes report loading and errors, so neither is left showing stale
// content while the other explains what happened.
func TestRenderPanelsBothReportLoadingAndErrors(t *testing.T) {
	loading := detailState{path: "/n", loading: true}
	if got := renderDataPanel(loading); !strings.Contains(got, "loading") {
		t.Errorf("renderDataPanel(loading) = %q, want a loading indicator", got)
	}
	if got := renderStatPanel(loading); !strings.Contains(got, "loading") {
		t.Errorf("renderStatPanel(loading) = %q, want a loading indicator", got)
	}

	failed := detailState{path: "/n", err: errors.New("zk: node does not exist")}
	if got := renderDataPanel(failed); !strings.Contains(got, "node does not exist") {
		t.Errorf("renderDataPanel(error) = %q, want the error message", got)
	}
	if got := renderStatPanel(failed); !strings.Contains(got, "node does not exist") {
		t.Errorf("renderStatPanel(error) = %q, want the error message", got)
	}
}
