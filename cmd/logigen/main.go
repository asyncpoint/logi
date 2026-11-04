// Command logigen generates synthetic log files and live log streams for
// trying logi and for the performance test plan (TDD §Performance Testing).
//
//	logigen -n 1000000 -o big.log                 # a static file
//	logigen -format json -n 50000 -o app.jsonl
//	logigen -format ncache -n 20000 -o ncache.log
//	logigen -live -rate 5000 -o live.log          # append 5000 records/s until Ctrl+C
//	logigen -live -rotate 100000 -o live.log      # rotate every 100k records
//	logigen -dir logs/ -files 3 -n 500            # three date-named files for directory mode
//	logigen -dir logs/ -live -roll-every 2000     # live: start the next day's file every 2000 records
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// dayFile names the file for a given day like the applications logi targets do
// (NCache: cache.2026-09-20.logs.txt; others: app.2026-09-20.log).
func dayFile(dir, format string, day time.Time) string {
	if format == "ncache" {
		return filepath.Join(dir, "cache."+day.Format("2006-01-02")+".logs.txt")
	}
	ext := ".log"
	if format == "json" {
		ext = ".jsonl"
	}
	return filepath.Join(dir, "app."+day.Format("2006-01-02")+ext)
}

var (
	sevs     = []string{"INFO", "INFO", "INFO", "INFO", "DEBUG", "WARN", "ERROR"}
	sevsLow  = []string{"info", "info", "info", "info", "debug", "warn", "error"}
	ncEvents = []string{"CacheInfo", "Cache.Add", "Cache.Insert", "ConnectionTable.GetConnection", "GMS.InstallView",
		"Message Manager", "TCP.start", "pb.ClientGmsImpl.determineCoord"}
	msgs = []string{"request completed", "cache hit", "cache miss", "connection established", "retrying operation",
		"item expired", "bulk insert finished", "client disconnected", "heartbeat", "configuration reloaded"}
)

// ncacheHeader is the column header NCache writes at the top of every log file.
const ncacheHeader = "TIMESTAMP                       APPDOMAIN                                       LOGGERNAME                                      THREADNAME                                      LEVEL           MESSAGE\n"

// writeHeader writes the per-file header some formats have.
func writeHeader(w io.Writer, format string) {
	if format == "ncache" {
		io.WriteString(w, ncacheHeader)
	}
}

// record writes one log record. With numbered, every message ends with its record
// number ("(item 12)") so individual records can be told apart; by default the
// messages look like real ones.
func record(w io.Writer, format string, i int, r *rand.Rand, t time.Time, multiline int, numbered bool) {
	k := r.Intn(len(sevs))
	msg := msgs[r.Intn(len(msgs))]
	switch format {
	case "json":
		fmt.Fprintf(w, `{"time":%q,"level":%q,"msg":"%s","request":{"id":"r-%d","ms":%d},"n":%d}`+"\n",
			t.Format(time.RFC3339Nano), sevsLow[k], msg, i, r.Intn(900), i)
	case "ncache":
		// NCache's real layout: fixed-width columns (32/48/48/48/16/40) under a header line.
		thread := ".NET TP Worker"
		if r.Intn(6) == 0 {
			thread = fmt.Sprint(r.Intn(90))
		}
		event := ncEvents[r.Intn(len(ncEvents))]
		if numbered {
			msg = fmt.Sprintf("%s (item %d)", msg, i)
		}
		fmt.Fprintf(w, "%-32s%-48s%-48s%-48s%-16s%-40s%s\n", t.Format("2006-01-02 15:04:05,000"),
			"Alachisoft.NCache.CacheHost", "demoCache", thread, sevs[k], event, msg)
		if sevs[k] == "ERROR" && multiline > 0 {
			fmt.Fprint(w, "Alachisoft.NCache.Runtime.Exceptions.OperationFailedException: operation failed\n   at Alachisoft.NCache.Server.Op.Execute()\n   at Alachisoft.NCache.Server.Pipeline.Run()\n")
		}
	default:
		worker, ms := r.Intn(16), r.Intn(900)
		if numbered {
			fmt.Fprintf(w, "%s %s [worker-%d] %s (item %d, %dms)\n", t.Format("2006-01-02 15:04:05,000"), sevs[k], worker, msg, i, ms)
		} else {
			fmt.Fprintf(w, "%s %s [worker-%d] %s (%dms)\n", t.Format("2006-01-02 15:04:05,000"), sevs[k], worker, msg, ms)
		}
		if sevs[k] == "ERROR" && multiline > 0 {
			fmt.Fprint(w, "System.InvalidOperationException: something went wrong\n   at App.Service.Handle()\n   at App.Program.Main()\n")
		}
	}
}

func main() {
	out := flag.String("o", "", "output file (default stdout for static mode)")
	n := flag.Int("n", 10000, "records to write (static mode)")
	format := flag.String("format", "text", "text | json | ncache")
	live := flag.Bool("live", false, "append forever instead of writing -n records")
	rate := flag.Int("rate", 1000, "live: records per second")
	rotate := flag.Int("rotate", 0, "live: rotate the file (rename to .1) every N records")
	multi := flag.Int("multiline", 1, "emit stack traces after ERROR records (0 = off)")
	numbered := flag.Bool("numbered", false, "end every message with its record number, e.g. \"(item 12)\", to tell records apart")
	seed := flag.Int64("seed", 1, "random seed")
	dir := flag.String("dir", "", "write date-named files into this directory (directory mode demos)")
	files := flag.Int("files", 3, "static -dir: number of daily files to write")
	rollEvery := flag.Int("roll-every", 0, "live -dir: start the next day's file every N records")
	flag.Parse()
	r := rand.New(rand.NewSource(*seed))
	base := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)

	if *dir != "" {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "logigen:", err)
			os.Exit(1)
		}
		if !*live {
			for d := 0; d < *files; d++ {
				day := base.AddDate(0, 0, d)
				f, err := os.Create(dayFile(*dir, *format, day))
				if err != nil {
					fmt.Fprintln(os.Stderr, "logigen:", err)
					os.Exit(1)
				}
				bw := bufio.NewWriterSize(f, 1<<20)
				writeHeader(bw, *format)
				for i := 0; i < *n; i++ {
					record(bw, *format, i, r, day.Add(time.Duration(i)*50*time.Millisecond), *multi, *numbered)
				}
				bw.Flush()
				f.Close()
			}
			return
		}
		day := time.Now()
		f, _ := os.OpenFile(dayFile(*dir, *format, day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if st, _ := f.Stat(); st != nil && st.Size() == 0 {
			writeHeader(f, *format)
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		perTick := max(1, *rate/50)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		i := 0
		for {
			select {
			case <-sig:
				f.Close()
				return
			case <-tick.C:
				for k := 0; k < perTick; k++ {
					record(f, *format, i, r, time.Now(), *multi, *numbered)
					i++
					if *rollEvery > 0 && i%*rollEvery == 0 {
						f.Close()
						day = day.AddDate(0, 0, 1) // the "next day": a brand-new file
						f, _ = os.OpenFile(dayFile(*dir, *format, day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
						writeHeader(f, *format)
					}
				}
			}
		}
	}

	if !*live {
		var w io.Writer = os.Stdout
		if *out != "" {
			f, err := os.Create(*out)
			if err != nil {
				fmt.Fprintln(os.Stderr, "logigen:", err)
				os.Exit(1)
			}
			defer f.Close()
			w = f
		}
		bw := bufio.NewWriterSize(w, 1<<20)
		writeHeader(bw, *format)
		for i := 0; i < *n; i++ {
			record(bw, *format, i, r, base.Add(time.Duration(i)*50*time.Millisecond), *multi, *numbered)
		}
		bw.Flush()
		return
	}

	if *out == "" {
		fmt.Fprintln(os.Stderr, "logigen: -live needs -o <file>")
		os.Exit(2)
	}
	open := func() *os.File {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "logigen:", err)
			os.Exit(1)
		}
		return f
	}
	f := open()
	if st, _ := f.Stat(); st != nil && st.Size() == 0 {
		writeHeader(f, *format)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	perTick := max(1, *rate/50)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	i := 0
	for {
		select {
		case <-sig:
			f.Close()
			return
		case <-tick.C:
			for k := 0; k < perTick; k++ {
				record(f, *format, i, r, time.Now(), *multi, *numbered)
				i++
				if *rotate > 0 && i%*rotate == 0 {
					f.Close()
					os.Rename(*out, *out+".1")
					f = open()
					writeHeader(f, *format)
				}
			}
		}
	}
}
