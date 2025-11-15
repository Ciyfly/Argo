package engine

type priorityItem struct {
	info  *UrlInfo
	depth int
	seq   int64
	index int
}

type priorityQueue []*priorityItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].depth == pq[j].depth {
		return pq[i].seq < pq[j].seq
	}
	return pq[i].depth < pq[j].depth
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	item := x.(*priorityItem)
	item.index = len(*pq)
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	if n == 0 {
		return nil
	}
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

func (pq priorityQueue) Peek() *priorityItem {
	if len(pq) == 0 {
		return nil
	}
	return pq[0]
}
