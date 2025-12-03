package arean

import (
	"arena"
	"runtime"
	"testing"
)

type Point struct {
	X []int
	Y []int
}

// go test example_test.go -tags=goexperiment.arenas -v
func TestAllowcate(t *testing.T) {
	// 创建一个 Arena
	ar := arena.NewArena()

	// 在 Arena 中批量分配对象
	points := make([]*Point, 0, 100)
	for i := 0; i < 100; i++ {
		// 使用 arena.New 分配内存，而不是普通的 new()
		p := arena.New[Point](ar)
		p.X = make([]int, 1024*1024)
		p.Y = make([]int, 1024*1024)
		points = append(points, p)
	}

	t.Log("First point:", points[0])

	// 手动释放 Arena 中的所有对象
	ar.Free()

	// 这里访问 points[0] 是不安全的（可能 panic 或返回垃圾值）
	t.Log(points[99]) // ❌ 释放后使用，风险很大
}

// go test -bench . -benchmem example_test.go example.go -tags=goexperiment.arenas
func getGCCount() uint32 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.NumGC
}

func BenchmarkNewAllowcate(b *testing.B) {
	startGC := getGCCount()
	for i := 0; i < b.N; i++ {
		points := make([]*Point, 100)
		for j := 0; j < 100; j++ {
			points[j] = &Point{
				X: make([]int, 1024*1024*10),
				Y: make([]int, 1024*1024*10),
			}
		}
	}
	encGC := getGCCount()
	b.Log("new gc count: ", encGC-startGC)
}

func BenchmarkAreanAllowcate(b *testing.B) {
	startGC := getGCCount()
	for i := 0; i < b.N; i++ {
		ar := arena.NewArena()
		points := make([]*Point, 100)
		for j := 0; j < 100; j++ {
			points[j] = arena.New[Point](ar)
			points[j].X = make([]int, 1024*1024*10)
			points[j].Y = make([]int, 1024*1024*10)

		}
		ar.Free()
	}

	encGC := getGCCount()
	b.Log("arena gc count: ", encGC-startGC)
}

/* output
goos: darwin
goarch: arm64
BenchmarkNewAllowcate-14              14          80965774 ns/op        16777222094 B/op             312 allocs/op
--- BENCH: BenchmarkNewAllowcate-14
    bench_test.go:28: new gc count:  99
    bench_test.go:28: new gc count:  895
    bench_test.go:28: new gc count:  1397
BenchmarkAreanAllowcate-14           348           3798507 ns/op        16777216065 B/op             202 allocs/op
--- BENCH: BenchmarkAreanAllowcate-14
    bench_test.go:46: arena gc count:  8
    bench_test.go:46: arena gc count:  6
    bench_test.go:46: arena gc count:  2
PASS
ok      command-line-arguments  4.339s
*/
