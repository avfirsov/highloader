package engine

import "container/heap"

type retryHeap []*logical

func (h retryHeap) Len() int { return len(h) }
func (h retryHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].seq < h[j].seq
	}
	return h[i].due.Before(h[j].due)
}
func (h retryHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i]; h[i].heapIndex = i; h[j].heapIndex = j }
func (h *retryHeap) Push(value any) { l := value.(*logical); l.heapIndex = len(*h); *h = append(*h, l) }
func (h *retryHeap) Pop() any {
	old := *h
	n := len(old)
	l := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	l.heapIndex = -1
	return l
}
func (r *Runner) nextRetry() *logical {
	if len(r.retries) == 0 || r.retries[0].due.After(r.now) {
		return nil
	}
	return r.retries[0]
}
func (r *Runner) removeRetry(l *logical) {
	if l.heapIndex >= 0 {
		heap.Remove(&r.retries, l.heapIndex)
	}
}
