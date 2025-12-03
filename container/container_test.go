package container

import (
	"container/heap"
	"container/ring"
	"fmt"
	"strconv"
	"testing"
)

type IntHeap []int

func (h IntHeap) Len() int           { return len(h) }
func (h IntHeap) Less(i, j int) bool { return h[i] < h[j] } // 最小堆
func (h IntHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *IntHeap) Push(x any) {
	*h = append(*h, x.(int))
}

func (h *IntHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func TestHeap(t *testing.T) {
	t.Run("heap sort", func(t *testing.T) {
		h := &IntHeap{2, 1, 5, 10, 100, 34, 0, 9, 4, 1, 23, 54, 7}
		heap.Init(h) // 初始最小堆

		// 堆排序
		// 思路：
		// 	1. 先把堆顶元素取出来，并和最后一个元素替换。同时堆容量减1
		// 	2. 堆顶的元素重新下沉调整到合适位置以保证堆顶元素依旧是最小的。
		//  3. 不断重复 2-3，直到堆为空

		var nums []int // 这里新增了一个数组，是因为 heap 包不支持动态调整堆大小
		for i := h.Len() - 1; i >= 0; i-- {
			val := heap.Pop(h).(int)
			nums = append(nums, val)

		}
		t.Log("after desc order", nums)
	})
}

type Node struct {
	ip string
	name string
}

func newNode(ip, name string) *Node {
	return  &Node{
		ip: ip,
		name: name,
	}
}

func TestRing(t *testing.T) {
	node := ring.New(10)

	index := 1
	node.Value = newNode(strconv.Itoa(index), fmt.Sprintf("node %d", index))
	index++

	for cur := node.Next(); cur != node; {
		cur.Value = newNode(strconv.Itoa(index), fmt.Sprintf("node %d", index))
		index++
		cur = cur.Next()
	}

	node.Do(func(a any) {
		node := a.(*Node)
		t.Logf("name: %s ip: %s\n", node.name,node.ip)
	})


	nodeCounts := make(map[string]int)
	// 负载均衡调用 100 次
	for i := 0; i < 100; i++ {
		cur := node
		actualNode := cur.Value.(*Node)

		nodeCounts[actualNode.ip]++
		node = cur.Next()
	}
	t.Log("invoke: ", nodeCounts)

}
