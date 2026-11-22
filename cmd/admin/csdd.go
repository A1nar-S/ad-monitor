package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/A1nar-S/advertisiment-monitoring/internal/csdd"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping/ss"
)

// csddInspections refreshes the model fail-rate table from CSDD's open
// inspection data: the newest years' files (about 230 MB a year), streamed,
// not kept. -file reads local copies instead, given as path:year.
func csddInspections(args []string) error {
	fs := flag.NewFlagSet("csdd-inspections", flag.ExitOnError)
	out := fs.String("out", "internal/deals/codebook/inspections.json", "where to write the table")
	years := fs.Int("years", 3, "how many of the newest inspection years to count")
	minN := fs.Int("min", 400, "drop a model's fuel with fewer main inspections")
	var files []string
	fs.Func("file", "a local file as path:year, instead of downloading; -years is then unused (repeatable)", func(s string) error {
		files = append(files, s)
		return nil
	})
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cb, err := ss.CarsCodebook()
	if err != nil {
		return err
	}
	makes := make([]csdd.Make, 0, len(cb.Makes))
	for _, m := range cb.Makes {
		cm := csdd.Make{Name: m.Name}
		for _, o := range m.Models {
			cm.Models = append(cm.Models, o.Name)
		}
		makes = append(makes, cm)
	}
	counter := csdd.NewCounter(makes)
	client := csdd.NewHTTPClient()

	if len(files) > 0 {
		for _, f := range files {
			path, y, ok := strings.Cut(f, ":")
			year, err := strconv.Atoi(y)
			if !ok || err != nil {
				return fmt.Errorf("-file %q: want path:year", f)
			}
			if err := countFile(counter, path, year); err != nil {
				return err
			}
		}
	} else {
		list, err := csdd.Files(ctx, client, csdd.PackageURL, *years)
		if err != nil {
			return err
		}
		for _, f := range list {
			if err := countURL(ctx, client, counter, f); err != nil {
				return err
			}
		}
	}

	table := counter.Table(*minN)
	raw, err := csdd.Marshal(table)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	slog.Info("wrote inspection table", "file", *out, "years", table.Years, "lines", counter.Read,
		"counted", counter.Counted, "models", len(table.Models), "bytes", len(raw))
	if counter.Counted*4 < counter.Read { // ~60% of lines are cars' main inspections
		slog.Warn("few lines counted: did the file format or the codebook change?")
	}
	return nil
}

func countFile(c *csdd.Counter, path string, year int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	slog.Info("counting", "file", path, "year", year)
	return c.Count(f, year)
}

func countURL(ctx context.Context, client *http.Client, c *csdd.Counter, f csdd.File) error {
	resp, err := csdd.Get(ctx, client, f.URL)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	slog.Info("counting", "url", f.URL, "year", f.Year)
	if err := c.Count(resp.Body, f.Year); err != nil {
		return fmt.Errorf("%s: %w", f.URL, err)
	}
	return nil
}
