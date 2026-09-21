package dotsync

import (
	"fmt"
	"strings"
)

// unifiedDiff renders a unified diff (3 lines of context) between two line slices.
// Dotfiles are small, so a plain O(n*m) LCS is fine.
func unifiedDiff(a, b []string, nameA, nameB string) string {
	n, m := len(a), len(b)
	if n*m > 25_000_000 {
		return fmt.Sprintf("--- %s\n+++ %s\nfiles differ (too large to diff here; %d vs %d lines)\n", nameA, nameB, n, m)
	}
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int
		bi   int
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i], i, j})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', a[i], i, j})
			i++
		default:
			ops = append(ops, op{'+', b[j], i, j})
			j++
		}
	}
	const ctx = 3
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", nameA, nameB)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(k-ctx, 0)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*ctx || run == len(ops) {
				end = min(end+ctx, len(ops))
				break
			}
			end = run
		}
		la, lb := 0, 0
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				la++
			}
			if o.kind != '-' {
				lb++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", ops[start].ai+1, la, ops[start].bi+1, lb)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = end
	}
	return out.String()
}
