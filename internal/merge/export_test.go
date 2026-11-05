package merge

// RuleCounts gives the number of rules in each phase.
func RuleCounts() []int {
	counts := make([]int, len(rules))
	for p := range rules {
		counts[p] = len(rules[p])
	}
	return counts
}

// RunInOrder merges like Run, but runs the rules of phase p in the order of order[p]: the
// indexes of the rules of that phase, from 0 to RuleCounts()[p]-1.
func RunInOrder(in Input, order [][]int) (*Result, error) {
	var phases [phaseCount][]rule
	for p := range rules {
		for _, i := range order[p] {
			phases[p] = append(phases[p], rules[p][i])
		}
	}
	return run(in, phases)
}
