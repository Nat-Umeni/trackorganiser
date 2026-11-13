package downloader

// TEMP(selection-log): this whole file is temporary. It records which YouTube
// result each search picked, so the wrong-song retry can be checked against a
// real run. Remove it, and every line marked TEMP(selection-log), once that is
// proven.

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// SelectionLog receives one block per downloaded track. Nil turns it off, which
// is what tests and a plain run get. Set once by main before any download
// starts, so the goroutines only ever read it.
var SelectionLog io.Writer

// selectionLogLock keeps blocks whole. With -jobs 16, sixteen tracks finish at
// once, and unlocked writes would interleave their lines.
var selectionLogLock sync.Mutex

// selectionRecord collects one track's searches, written out in one go at the
// end. A nil record means logging is off, and every method is then a no-op.
type selectionRecord struct {
	fileName string
	builder  strings.Builder
	kept     int
	keptInfo videoInfo

	// deeper is set when the kept download came from a listing rather than a
	// search's first result.
	deeper bool
}

// note adds a free-form line - the deeper pick's listings use it.
func (r *selectionRecord) note(format string, args ...any) {
	if r == nil {
		return
	}

	fmt.Fprintf(&r.builder, "  "+format+"\n", args...)
}

// discard records that what was picked was thrown away, not kept.
func (r *selectionRecord) discard() {
	if r == nil {
		return
	}

	r.kept = -1
	r.deeper = false
}

// keepDeeper records that a result from a listing replaced the first pick.
func (r *selectionRecord) keepDeeper(info videoInfo) {
	if r == nil {
		return
	}

	r.deeper = true
	r.keptInfo = info
}

func newSelectionRecord(req Request) *selectionRecord {
	if SelectionLog == nil {
		return nil
	}

	record := &selectionRecord{fileName: req.FileName, kept: -1}
	fmt.Fprintf(&record.builder, "%s   [Spotify: %q, %s]\n",
		req.FileName, req.Tags.Title, time.Duration(req.DurationMS)*time.Millisecond)

	return record
}

// verdictFor names which check failed, so the log shows whether the title or
// the artist was the giveaway.
func verdictFor(titleOK, artistOK, standIn bool) string {
	switch {
	case !titleOK && !artistOK:
		return "title WRONG, artist WRONG"
	case !titleOK:
		return "title WRONG"
	case !artistOK && standIn:
		return "ok (length stands in for the artist)"
	case !artistOK:
		return "artist WRONG"
	default:
		return "ok"
	}
}

// attempt records one search: what it found and whether it passed, or why it
// found nothing.
func (r *selectionRecord) attempt(index int, query string, info videoInfo, err error, verdict string) {
	if r == nil {
		return
	}

	if err != nil {
		fmt.Fprintf(&r.builder, "  %d  %q -> FAILED: %s\n", index+1, query, oneLine(err))
		return
	}

	fmt.Fprintf(&r.builder, "  %d  %q -> %s | %s | %ss | %s | %s\n",
		index+1, query, info.Title, info.Channel, info.Duration, info.ID, verdict)
}

func (r *selectionRecord) keep(index int, info videoInfo) {
	if r == nil {
		return
	}

	r.kept = index
	r.keptInfo = info
}

// finish writes the block with the track's final result. Anything other than
// the first search being kept also gets a line in the terminal, since that is
// the event worth noticing during the run.
func (r *selectionRecord) finish(err error) {
	if r == nil {
		return
	}

	result := "ok"
	if err != nil {
		result = oneLine(err)
	}

	switch {
	case r.deeper:
		fmt.Fprintf(&r.builder, "  kept the deeper pick   result: %s\n\n", result)
	case r.kept >= 0:
		fmt.Fprintf(&r.builder, "  kept #%d   result: %s\n\n", r.kept+1, result)
	default:
		fmt.Fprintf(&r.builder, "  kept nothing   result: %s\n\n", result)
	}

	selectionLogLock.Lock()
	io.WriteString(SelectionLog, r.builder.String())
	selectionLogLock.Unlock()

	switch {
	case r.deeper:
		fmt.Printf("Found a closer match for %s: %q (%s)\n",
			r.fileName, r.keptInfo.Title, r.keptInfo.Channel)
	case r.kept > 0:
		fmt.Printf("Retried %s: kept %q (%s) from search %d\n",
			r.fileName, r.keptInfo.Title, r.keptInfo.Channel, r.kept+1)
	}
}

// oneLine flattens an error onto one line - yt-dlp failures carry its whole
// output - and caps it, so one bad track can't bury the rest of the log.
func oneLine(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 300 {
		text = text[:300] + "..."
	}

	return text
}
