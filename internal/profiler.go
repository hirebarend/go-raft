package internal

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/*
	"runtime"
	"strconv"
)

func StartProfiler() {
	http.HandleFunc("GET /debug/pprof/config", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if v := q.Get("mutex"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				runtime.SetMutexProfileFraction(n)
			}
		}

		if v := q.Get("block"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				runtime.SetBlockProfileRate(n)
			}
		}

		fmt.Fprintln(w, "ok")
	})

	go func() {
		log.Println("pprof: http://127.0.0.1:6060/debug/pprof/")
		_ = http.ListenAndServe("127.0.0.1:6060", nil)
	}()
}
