package main

import (
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

func read(fp *os.File, off int64) {
	log.Print("read")
	buf := make([]byte, 1<<20)
	for i := 0; i < 4*1024; i++ {
		n, err := fp.ReadAt(buf, off)
		if err != nil || n != 1<<20 {
			log.Fatalf("n %d err %s", n, err)
		}
		off += 1 << 20
	}
}

func main() {
	fp, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	p, _ := strconv.Atoi(os.Args[2])
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < p; i++ {
		wg.Add(1)
		go func(i int64) {
			read(fp, i*4<<30)
			wg.Done()
		}(int64(i))
	}
	wg.Wait()
	
	log.Println("time cost:", time.Since(start).Seconds())

}
