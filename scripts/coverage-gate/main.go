// Command coverage-gate reports and compares Go coverprofiles for Mihari CI.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// criticalPackages are the design-doc load-bearing packages compared for relative regression.
// Keep this list in code so workflow and tool share one source of truth.
var criticalPackages = []string{
	"github.com/mihari-proxy/mihari/internal/config",
	"github.com/mihari-proxy/mihari/internal/control/client",
	"github.com/mihari-proxy/mihari/internal/control/protocol",
	"github.com/mihari-proxy/mihari/internal/control/server",
	"github.com/mihari-proxy/mihari/internal/daemon",
	"github.com/mihari-proxy/mihari/internal/runtime",
	"github.com/mihari-proxy/mihari/internal/state",
	"github.com/mihari-proxy/mihari/internal/subscription",
	"github.com/mihari-proxy/mihari/internal/supervisor",
	"github.com/mihari-proxy/mihari/internal/web",
}

type coverage struct {
	Covered    uint64
	Statements uint64
}

func (c coverage) percent() (float64, bool) {
	if c.Statements == 0 {
		return 0, false
	}
	return 100 * float64(c.Covered) / float64(c.Statements), true
}

type report struct {
	Total    coverage
	Packages map[string]coverage
}

type policy struct {
	TotalDrop    float64
	CriticalDrop float64
	Critical     []string
}

type result struct {
	TotalDelta    float64
	PackageDeltas map[string]float64
	Violations    []string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: coverage-gate <report|compare> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "report":
		os.Exit(runReport(os.Args[2:]))
	case "compare":
		os.Exit(runCompare(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	profile := fs.String("profile", "", "path to coverprofile")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil || *profile == "" {
		fmt.Fprintln(os.Stderr, "usage: coverage-gate report -profile <path>")
		return 2
	}
	rep, err := loadProfile(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(formatReport(rep))
	return 0
}

func runCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	basePath := fs.String("base", "", "base coverprofile")
	headPath := fs.String("head", "", "head coverprofile")
	totalDrop := fs.Float64("total-drop", 0.5, "allowed total percentage-point drop")
	criticalDrop := fs.Float64("critical-drop", 1.0, "allowed critical package percentage-point drop")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil || *basePath == "" || *headPath == "" {
		fmt.Fprintln(os.Stderr, "usage: coverage-gate compare -base <path> -head <path> [-total-drop 0.5] [-critical-drop 1.0]")
		return 2
	}
	base, err := loadProfile(*basePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "base:", err)
		return 1
	}
	head, err := loadProfile(*headPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "head:", err)
		return 1
	}
	res := compare(base, head, policy{
		TotalDrop:    *totalDrop,
		CriticalDrop: *criticalDrop,
		Critical:     criticalPackages,
	})
	fmt.Print(formatCompare(base, head, res))
	if len(res.Violations) > 0 {
		return 1
	}
	return 0
}

func loadProfile(path string) (report, error) {
	f, err := os.Open(path)
	if err != nil {
		return report{}, err
	}
	defer f.Close()
	return parseProfile(f)
}

func parseProfile(r io.Reader) (report, error) {
	scanner := bufio.NewScanner(r)
	// Coverprofiles can have long paths; raise the token limit modestly.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		modeSeen bool
		out      = report{Packages: make(map[string]coverage)}
	)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode:") {
			if modeSeen {
				return report{}, fmt.Errorf("line %d: duplicate mode header", lineNo)
			}
			mode := strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			if mode == "" {
				return report{}, fmt.Errorf("line %d: empty mode header", lineNo)
			}
			modeSeen = true
			continue
		}
		if !modeSeen {
			return report{}, fmt.Errorf("line %d: missing mode header", lineNo)
		}
		// file:startLine.startCol,endLine.endCol numStmt count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return report{}, fmt.Errorf("line %d: malformed block", lineNo)
		}
		filePart := fields[0]
		numStmt, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return report{}, fmt.Errorf("line %d: invalid statement count", lineNo)
		}
		count, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return report{}, fmt.Errorf("line %d: invalid execution count", lineNo)
		}
		// Reject negative-looking values that parse as huge unsigned after a leading '-'.
		if strings.HasPrefix(fields[1], "-") || strings.HasPrefix(fields[2], "-") {
			return report{}, fmt.Errorf("line %d: negative values are not allowed", lineNo)
		}
		colon := strings.LastIndex(filePart, ":")
		if colon <= 0 {
			return report{}, fmt.Errorf("line %d: missing file:range", lineNo)
		}
		filePath := filePart[:colon]
		pkg := packageOf(filePath)
		if pkg == "" {
			return report{}, fmt.Errorf("line %d: cannot derive package from %q", lineNo, filePath)
		}
		block := coverage{Statements: numStmt}
		if count > 0 {
			block.Covered = numStmt
		}
		out.Total.Statements += block.Statements
		out.Total.Covered += block.Covered
		cur := out.Packages[pkg]
		cur.Statements += block.Statements
		cur.Covered += block.Covered
		out.Packages[pkg] = cur
	}
	if err := scanner.Err(); err != nil {
		return report{}, err
	}
	if !modeSeen {
		return report{}, fmt.Errorf("empty or missing coverprofile mode header")
	}
	return out, nil
}

func packageOf(filePath string) string {
	// Normalize Windows separators so package identity is stable across runners.
	normalized := strings.ReplaceAll(filePath, "\\", "/")
	dir := path.Dir(normalized)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

func compare(base, head report, p policy) result {
	res := result{PackageDeltas: make(map[string]float64)}
	basePct, baseOK := base.Total.percent()
	headPct, headOK := head.Total.percent()
	if baseOK && headOK {
		res.TotalDelta = headPct - basePct
		if res.TotalDelta < -p.TotalDrop {
			res.Violations = append(res.Violations,
				fmt.Sprintf("total coverage dropped by %.2fpp (allowed %.2fpp): base=%.2f%% head=%.2f%%",
					-res.TotalDelta, p.TotalDrop, basePct, headPct))
		}
	} else if baseOK && !headOK {
		res.Violations = append(res.Violations, "head total coverage is n/a while base has statements")
	}

	seen := make(map[string]struct{})
	for _, pkg := range p.Critical {
		seen[pkg] = struct{}{}
		b, bHas := base.Packages[pkg]
		h, hHas := head.Packages[pkg]
		switch {
		case bHas && !hHas:
			res.Violations = append(res.Violations, fmt.Sprintf("critical package missing in head: %s", pkg))
			continue
		case !bHas && hHas:
			// New critical package in head: improvement / expansion, not a violation.
			hp, ok := h.percent()
			if ok {
				res.PackageDeltas[pkg] = hp
			}
			continue
		case !bHas && !hHas:
			continue
		}
		bp, bOK := b.percent()
		hp, hOK := h.percent()
		if !bOK || !hOK {
			if bOK && !hOK {
				res.Violations = append(res.Violations, fmt.Sprintf("critical package %s head coverage is n/a", pkg))
			}
			continue
		}
		delta := hp - bp
		res.PackageDeltas[pkg] = delta
		if delta < -p.CriticalDrop {
			res.Violations = append(res.Violations,
				fmt.Sprintf("critical package %s dropped by %.2fpp (allowed %.2fpp): base=%.2f%% head=%.2f%%",
					pkg, -delta, p.CriticalDrop, bp, hp))
		}
	}
	// Stable violation order.
	sort.Strings(res.Violations)
	return res
}

func formatReport(rep report) string {
	var b strings.Builder
	b.WriteString("## Coverage report\n\n")
	fmt.Fprintf(&b, "- **total**: %s\n\n", formatCoverage(rep.Total))
	b.WriteString("| package | coverage |\n|---|---|\n")
	for _, pkg := range criticalPackages {
		cov, ok := rep.Packages[pkg]
		if !ok {
			fmt.Fprintf(&b, "| `%s` | n/a |\n", shortPkg(pkg))
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", shortPkg(pkg), formatCoverage(cov))
	}
	b.WriteString("\n")
	return b.String()
}

func formatCompare(base, head report, res result) string {
	var b strings.Builder
	b.WriteString("## Coverage compare\n\n")
	fmt.Fprintf(&b, "- **base total**: %s\n", formatCoverage(base.Total))
	fmt.Fprintf(&b, "- **head total**: %s\n", formatCoverage(head.Total))
	if _, ok := base.Total.percent(); ok {
		if _, ok := head.Total.percent(); ok {
			fmt.Fprintf(&b, "- **total delta**: %+.2fpp\n", res.TotalDelta)
		}
	}
	b.WriteString("\n| package | base | head | delta |\n|---|---|---|---|\n")
	for _, pkg := range criticalPackages {
		bc, bOK := base.Packages[pkg]
		hc, hOK := head.Packages[pkg]
		baseS, headS := "n/a", "n/a"
		deltaS := "n/a"
		if bOK {
			baseS = formatCoverage(bc)
		}
		if hOK {
			headS = formatCoverage(hc)
		}
		if d, ok := res.PackageDeltas[pkg]; ok && bOK && hOK {
			deltaS = fmt.Sprintf("%+.2fpp", d)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", shortPkg(pkg), baseS, headS, deltaS)
	}
	if len(res.Violations) == 0 {
		b.WriteString("\n**result**: pass\n")
	} else {
		b.WriteString("\n**result**: fail\n\n")
		for _, v := range res.Violations {
			fmt.Fprintf(&b, "- %s\n", v)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func formatCoverage(c coverage) string {
	pct, ok := c.percent()
	if !ok {
		return "n/a"
	}
	return fmt.Sprintf("%.2f%% (%d/%d)", pct, c.Covered, c.Statements)
}

func shortPkg(pkg string) string {
	const prefix = "github.com/mihari-proxy/mihari/"
	return strings.TrimPrefix(pkg, prefix)
}
