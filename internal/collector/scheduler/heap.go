package scheduler

type scheduleHeap []extensionGroup

func (h scheduleHeap) Len() int {
	return len(h)
}

func (h scheduleHeap) Less(i, j int) bool {
	return h[i].NextRunAt.Before(h[j].NextRunAt)
}

func (h scheduleHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *scheduleHeap) Push(value any) {
	*h = append(*h, value.(extensionGroup))
}

func (h *scheduleHeap) Pop() any {
	old := *h
	lastIndex := len(old) - 1
	group := old[lastIndex]
	old[lastIndex] = extensionGroup{}
	*h = old[:lastIndex]
	return group
}
