package diff

// Line diff of two texts (canonical JSON of two snapshots, or degraded-mode page texts)
// for the side-by-side view: Myers' algorithm on the part between the common prefix and
// suffix, bounded so that a huge rewrite cannot exhaust memory.

const (
	maxEditDistance = 2000 // beyond, the texts are reported as too different to display
	maxDiffLines    = 4000 // lines returned in hunks (context included)
)

type DiffLine struct {
	Op   string `json:"op"`            // "=" context, "-" removed, "+" added
	Old  int    `json:"old,omitempty"` // 1-based line number in the old text
	New  int    `json:"new,omitempty"` // 1-based line number in the new text
	Text string `json:"text"`
}

type Hunk struct {
	OldStart int        `json:"old_start"`
	NewStart int        `json:"new_start"`
	Lines    []DiffLine `json:"lines"`
}

type TextDiff struct {
	Hunks     []Hunk `json:"hunks"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Truncated bool   `json:"truncated,omitempty"` // more than maxDiffLines lines to show
	TooLarge  bool   `json:"too_large,omitempty"` // edit distance above maxEditDistance: no hunks
}

// LineDiff compares old and new line by line and groups the changes in hunks with
// context lines around them.
func LineDiff(old, cur []string, context int) *TextDiff {
	ops, ok := editScript(old, cur)
	td := &TextDiff{Hunks: []Hunk{}}
	if !ok {
		td.TooLarge = true
		return td
	}
	for _, o := range ops {
		switch o.Op {
		case "+":
			td.Added++
		case "-":
			td.Removed++
		}
	}
	// hunks: changed lines plus `context` equal lines around them
	keep := make([]bool, len(ops))
	for i, o := range ops {
		if o.Op == "=" {
			continue
		}
		for j := max(0, i-context); j <= min(len(ops)-1, i+context); j++ {
			keep[j] = true
		}
	}
	shown := 0
	for i := 0; i < len(ops); i++ {
		if !keep[i] {
			continue
		}
		h := Hunk{OldStart: firstNonZero(ops[i].Old, ops[i].oldBefore), NewStart: firstNonZero(ops[i].New, ops[i].newBefore)}
		for ; i < len(ops) && keep[i]; i++ {
			if shown == maxDiffLines {
				td.Truncated = true
				break
			}
			h.Lines = append(h.Lines, ops[i].DiffLine)
			shown++
		}
		td.Hunks = append(td.Hunks, h)
		if td.Truncated {
			break
		}
	}
	return td
}

type op struct {
	DiffLine
	oldBefore, newBefore int // position before which an insertion/deletion happens (hunk start)
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// editScript returns the full sequence of equal/removed/added lines, or false when the
// edit distance exceeds maxEditDistance.
func editScript(a, b []string) ([]op, bool) {
	// common prefix and suffix are equal lines: no need to run Myers on them
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	mid, ok := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	if !ok {
		return nil, false
	}
	ops := make([]op, 0, len(a)+len(b))
	oi, ni := 0, 0 // lines consumed so far (0-based)
	emit := func(kind string) {
		o := op{DiffLine: DiffLine{Op: kind}, oldBefore: oi + 1, newBefore: ni + 1}
		switch kind {
		case "=":
			o.Old, o.New, o.Text = oi+1, ni+1, a[oi]
			oi++
			ni++
		case "-":
			o.Old, o.Text = oi+1, a[oi]
			oi++
		case "+":
			o.New, o.Text = ni+1, b[ni]
			ni++
		}
		ops = append(ops, o)
	}
	for range pre {
		emit("=")
	}
	for _, k := range mid {
		emit(k)
	}
	for range suf {
		emit("=")
	}
	return ops, true
}

// myers returns the edit script ("=", "-", "+" per line) turning a into b.
func myers(a, b []string) ([]string, bool) {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		script := make([]string, 0, n+m)
		for range n {
			script = append(script, "-")
		}
		for range m {
			script = append(script, "+")
		}
		return script, true
	}
	// lines as integers: comparisons in the inner loop stay cheap
	ids := map[string]int32{}
	intern := func(lines []string) []int32 {
		out := make([]int32, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = int32(len(ids))
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	x1, x2 := intern(a), intern(b)

	maxD := min(n+m, maxEditDistance)
	off := maxD + 1
	v := make([]int32, 2*off+1)
	var trace [][]int32 // trace[d] = v[k] for k in [-d-1, d+1] before step d
	for d := 0; d <= maxD; d++ {
		snap := make([]int32, 2*d+3)
		copy(snap, v[off-d-1:off+d+2])
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = int(v[off+k+1])
			} else {
				x = int(v[off+k-1]) + 1
			}
			y := x - k
			for x < n && y < m && x1[x] == x2[y] {
				x++
				y++
			}
			v[off+k] = int32(x)
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int32, n, m int) []string {
	var rev []string
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		snap := trace[d]
		at := func(k int) int { return int(snap[k+d+1]) } // snap covers k in [-d-1, d+1]
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			rev = append(rev, "=")
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				rev = append(rev, "+")
			} else {
				rev = append(rev, "-")
			}
		}
		x, y = prevX, prevY
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
