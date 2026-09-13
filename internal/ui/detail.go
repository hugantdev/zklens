package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hugantdev/zklens/internal/zk"
)

// detailState is the transient result of fetching one znode's data and
// Stat for the detail view. Unlike node's children cache, it is not kept
// once the user leaves the detail view: reopening it always fetches fresh
// data, since a znode's data can change at any time. watching mirrors
// node.watching but for the data-watch toggled with 'w' while this panel
// is open; it is cleared whenever the panel is (re)opened or left, so a
// live watch never keeps running unobserved in the background.
type detailState struct {
	path                string
	loading             bool
	data                []byte
	stat                *zk.Stat
	err                 error
	watching            bool
	dataWatchGeneration uint64
	dataWatchCancel     chan struct{}
	statWatchGeneration uint64
	statWatchCancel     chan struct{}
}

// detailHeader renders the path title and, when armed, the watch marker.
func detailHeader(d detailState) string {
	out := styles.Title.Render(d.path)
	if d.watching {
		// Independently rendered and concatenated, not nested inside the
		// Title segment above — see delegate.go's Render for why.
		out += " " + styles.Marker.Render("◆ watch")
	}
	return out
}

// detailStatus returns the text that stands in for a znode's content while it
// is loading or after it failed to load, or "" when there is content to show.
func detailStatus(d detailState) string {
	switch {
	case d.loading:
		return styles.Muted.Render("loading…")
	case d.err != nil:
		return styles.NodeError.Render(d.err.Error())
	default:
		return ""
	}
}

// renderDetail formats data and Stat as one block, headed by the path: the raw
// data, its decoded form when it is valid text or JSON, and the complete Stat.
func renderDetail(d detailState) string {
	return detailHeader(d) + "\n\n" + renderMergedPanel(d)
}

// renderMergedPanel is renderDetail without the path header, for the single
// pane a terminal too short to split the right column falls back to — there
// the panel's own frame already carries the path.
func renderMergedPanel(d detailState) string {
	if d.path == "" {
		return renderDataPanel(d)
	}
	if status := detailStatus(d); status != "" {
		return status
	}

	var b strings.Builder
	b.WriteString(styles.SectionTitle.Render("Data"))
	b.WriteString("\n")
	b.WriteString(formatData(d.data))
	b.WriteString("\n\n")
	b.WriteString(styles.SectionTitle.Render("Stat"))
	b.WriteString("\n")
	b.WriteString(formatStat(d.stat))
	return b.String()
}

// renderDataPanel renders just the data half, for the Data pane. The pane is
// always on screen, so unlike renderDetail it has an "empty" state: nothing is
// fetched until the user presses tab on a node.
func renderDataPanel(d detailState) string {
	if d.path == "" {
		return styles.Placeholder.Render("(press tab to load)")
	}
	if status := detailStatus(d); status != "" {
		return status
	}
	return formatData(d.data)
}

// renderStatPanel renders just the Stat half, for the Stat pane.
func renderStatPanel(d detailState) string {
	if d.path == "" {
		return styles.Placeholder.Render("(no node loaded)")
	}
	if status := detailStatus(d); status != "" {
		return status
	}
	return formatStat(d.stat)
}

// formatData shows data's raw bytes, and additionally its decoded form
// when that's meaningful: pretty-printed when it parses as JSON, or noted
// as plain text when it's valid UTF-8 but not JSON. Data that isn't valid
// UTF-8, or contains terminal control characters, is shown only as a hex dump
// — printing arbitrary bytes straight to the terminal risks garbling the
// display or executing terminal escape sequences.
func formatData(data []byte) string {
	if len(data) == 0 {
		return styles.Muted.Render("(no data)")
	}

	if !isSafeTerminalText(data) {
		var b strings.Builder
		b.WriteString(styles.Label.Render("Raw (binary or control bytes, hexdump):"))
		b.WriteString("\n")
		b.WriteString(hexDump(data))
		return b.String()
	}

	var b strings.Builder
	b.WriteString(styles.Label.Render("Raw:"))
	b.WriteString("\n")
	b.WriteString(string(data))

	if json.Valid(data) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err == nil {
			b.WriteString("\n\n")
			b.WriteString(styles.Label.Render("Decoded (JSON):"))
			b.WriteString("\n")
			b.WriteString(pretty.String())
		}
	}

	return b.String()
}

// isSafeTerminalText reports whether data can be written to the terminal as
// text. Newlines are retained so ordinary multi-line data stays readable; all
// other Unicode control characters, including ESC and C1 controls such as
// CSI, are rendered as bytes instead.
func isSafeTerminalText(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}

	for _, r := range string(data) {
		if r != '\n' && unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// hexDump renders data as a classic 16-bytes-per-line offset/hex/ASCII
// dump.
func hexDump(data []byte) string {
	const width = 16
	var b strings.Builder
	for i := 0; i < len(data); i += width {
		end := i + width
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]

		fmt.Fprintf(&b, "%08x  ", i)
		for j := 0; j < width; j++ {
			if j < len(chunk) {
				fmt.Fprintf(&b, "%02x ", chunk[j])
			} else {
				b.WriteString("   ")
			}
			if j == width/2-1 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(" |")
		for _, c := range chunk {
			if c >= 32 && c < 127 {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|")
		if end < len(data) {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// formatStat renders the full Stat as one label/value line per field, in
// the order requested by the detail view's spec.
func formatStat(s *zk.Stat) string {
	if s == nil {
		return styles.Muted.Render("(no stat)")
	}

	rows := [][2]string{
		{"czxid", fmt.Sprintf("%d", s.Czxid)},
		{"mzxid", fmt.Sprintf("%d", s.Mzxid)},
		{"ctime", formatZKTime(s.Ctime)},
		{"mtime", formatZKTime(s.Mtime)},
		{"version", fmt.Sprintf("%d", s.Version)},
		{"cversion", fmt.Sprintf("%d", s.Cversion)},
		{"aversion", fmt.Sprintf("%d", s.Aversion)},
		{"ephemeralOwner", fmt.Sprintf("%d", s.EphemeralOwner)},
		{"dataLength", fmt.Sprintf("%d", s.DataLength)},
		{"numChildren", fmt.Sprintf("%d", s.NumChildren)},
		{"pzxid", fmt.Sprintf("%d", s.Pzxid)},
	}

	var b strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&b, "%-15s %s", row[0]+":", row[1])
		if i < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// formatZKTime renders a Stat millisecond-since-epoch timestamp alongside
// a human-readable form.
func formatZKTime(ms int64) string {
	return fmt.Sprintf("%d (%s)", ms, time.UnixMilli(ms).Format(time.RFC3339))
}
