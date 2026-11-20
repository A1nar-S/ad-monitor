package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/app"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/config"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/kafka"
)

// dlqTopics are the topics services consume, so the ones that have dead letters.
var dlqTopics = []string{events.TopicListingsDiscovered, events.TopicListingsNew, events.TopicNotificationsRequested}

const dlqUsage = "usage: admin dlq list [-limit 50] <topic> | dlq replay <topic>   (topic: " +
	events.TopicListingsDiscovered + ", " + events.TopicListingsNew + ", " + events.TopicNotificationsRequested + ")"

// dlq inspects and replays dead-lettered records: those a consumer could not
// handle or decode. It needs only Kafka.
func dlq(args []string) error {
	if len(args) < 1 {
		return errors.New(dlqUsage)
	}
	fs := flag.NewFlagSet("dlq "+args[0], flag.ExitOnError)
	limit := fs.Int("limit", 50, "list: show at most this many records")
	_ = fs.Parse(args[1:])
	if fs.NArg() != 1 || !slices.Contains(dlqTopics, fs.Arg(0)) {
		return errors.New(dlqUsage)
	}
	if *limit < 1 {
		return errors.New("-limit must be at least 1")
	}
	name := fs.Arg(0)

	cfg, err := config.AdminFromEnv()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// A broker that is down must fail the command, not hang it.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	topic, group := cfg.Topic(name), app.DLQReplayGroup(cfg.Common, name)

	switch args[0] {
	case "list":
		items, total, err := kafka.ListDeadLetters(ctx, cfg.KafkaBrokers, topic, group, *limit)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d dead letters waiting (not replayed)\n", kafka.DLQTopic(topic), total)
		for _, d := range items {
			fmt.Printf("p%d@%d  %s  %s  key=%q  %d bytes  group=%s\n    %s\n",
				d.Partition, d.Offset, d.FailedAt, d.Reason, d.Key, d.Size, d.Group, oneLine(d.Error))
		}
		if int64(len(items)) < total {
			fmt.Printf("... and %d more (raise -limit)\n", total-int64(len(items)))
		}
		return nil

	case "replay":
		n, err := kafka.ReplayDeadLetters(ctx, cfg.KafkaBrokers, topic, group)
		fmt.Printf("replayed %d records from %s to %s\n", n, kafka.DLQTopic(topic), topic)
		return err
	}
	return errors.New(dlqUsage)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
