// Package telegrambot puts the platform-neutral bot on Telegram: commands
// written as /watch cars name=Cheap Skoda price_max=7000, replies with
// inline buttons for menus. It answers private chats only, and is the bot's
// bot.Messenger on Telegram.
package telegrambot

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/bot"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/telegram"
)

// Platform is the name the bot and the notifier know Telegram by.
const Platform = "telegram"

const (
	pollTimeout = 50 * time.Second
	// maxReply leaves room under Telegram's 4096 characters for the markup.
	maxReply   = 3800
	maxButtons = 30 // rows of buttons under one reply
	// workers handle updates at once; one chat's always go to the same
	// worker, so they are answered in order.
	workers = 8
)

// Adapter connects a bot.Bot to Telegram.
type Adapter struct {
	c    *telegram.Client
	core *bot.Bot
	cmds map[string]bot.Command
}

func New(c *telegram.Client) *Adapter { return &Adapter{c: c} }

// --- bot.Messenger ----------------------------------------------------------

func (a *Adapter) Platform() string { return Platform }

// DirectChat: a private chat's ID is the user's.
func (a *Adapter) DirectChat(_ context.Context, userID string) (string, error) { return userID, nil }

func (a *Adapter) Send(ctx context.Context, target, text string) error {
	_, err := a.c.SendMessage(ctx, target, telegram.HTML(text), nil, false)
	return err
}

// Mention: the bot only posts in private chats here.
func (a *Adapter) Mention(string) string { return "this chat" }

func (a *Adapter) ChannelName(context.Context, string) string { return "" }

// --- serving ------------------------------------------------------------------

// Serve sets the command menu and answers messages until ctx is done.
func (a *Adapter) Serve(ctx context.Context, core *bot.Bot) error {
	a.core = core
	a.cmds = map[string]bot.Command{}
	for _, c := range core.Commands() {
		a.cmds[c.Name] = c
	}
	me, err := a.c.GetMe(ctx)
	if err != nil {
		return err
	}
	if err := a.c.SetMyCommands(ctx, menu(core.Commands())); err != nil {
		slog.Warn("telegram command menu", "err", err)
	}
	slog.Info("telegram bot ready", "username", me.Username)

	// Updates already taken are finished on shutdown, not cut off: the
	// last offset confirms them to Telegram.
	work := context.WithoutCancel(ctx)
	queues := make([]chan telegram.Update, workers)
	var wg sync.WaitGroup
	for i := range queues {
		queues[i] = make(chan telegram.Update, 100)
		wg.Go(func() {
			for u := range queues[i] {
				a.handle(work, u)
			}
		})
	}
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := a.c.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			slog.Warn("telegram updates", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			offset = u.UpdateID + 1
			queues[chatOf(u)%workers] <- u
		}
	}
	for _, q := range queues {
		close(q)
	}
	wg.Wait()
	// Confirm the last updates, so a restart doesn't answer them again.
	if offset > 0 {
		ack, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = a.c.GetUpdates(ack, offset, 0)
	}
	return nil
}

// chatOf picks an update's queue: by its chat (a button's user).
func chatOf(u telegram.Update) uint64 {
	var id int64
	switch {
	case u.Message != nil:
		id = u.Message.Chat.ID
	case u.CallbackQuery != nil:
		id = u.CallbackQuery.From.ID
	}
	return uint64(id) // a hash key: the sign does not matter
}

func (a *Adapter) handle(ctx context.Context, u telegram.Update) {
	var err error
	switch {
	case u.CallbackQuery != nil:
		err = a.button(ctx, u.CallbackQuery)
	case u.Message != nil && u.Message.From != nil:
		err = a.message(ctx, u.Message)
	}
	if err != nil {
		slog.Error("telegram update", "id", u.UpdateID, "err", err)
	}
}

func (a *Adapter) message(ctx context.Context, m *telegram.Message) error {
	chat := strconv.FormatInt(m.Chat.ID, 10)
	if m.Chat.Type != "private" {
		if !strings.HasPrefix(m.Text, "/") {
			return nil
		}
		return a.reply(ctx, chat, bot.Reply{Text: "I only work in private chats for now: message me directly."})
	}
	if !strings.HasPrefix(strings.TrimSpace(m.Text), "/") {
		return a.reply(ctx, chat, bot.Reply{Text: "Send /help to see what I can do."})
	}
	user := userOf(*m.From)
	p, err := Parse(a.cmds, m.Text, chat)
	if err != nil {
		text := err.Error()
		// A missing search name: list the user's own, as Discord's autocomplete would.
		if err.missing == "name" && err.complete {
			if names := a.core.Complete(ctx, user, p.Call, "name"); len(names) > 0 {
				text += "\nYour searches: " + strings.Join(names, ", ")
			}
		}
		return a.reply(ctx, chat, bot.Reply{Text: text})
	}
	if p.Call.Command == cmdHelp || p.Call.Command == cmdStart {
		return a.reply(ctx, chat, bot.Reply{Text: Help(a.core.Commands())})
	}
	return a.reply(ctx, chat, a.core.Run(ctx, user, p.Call))
}

// button runs a menu choice and updates the message it was under.
func (a *Adapter) button(ctx context.Context, q *telegram.CallbackQuery) error {
	defer func() { _ = a.c.AnswerCallbackQuery(ctx, q.ID) }()
	if q.Message == nil {
		return nil
	}
	action, value, _ := strings.Cut(q.Data, ":")
	text, kb := render(a.core.Act(ctx, userOf(q.From), action, value))
	err := a.c.EditMessageText(ctx, q.Message.Chat.ID, q.Message.MessageID, text, kb)
	var te *telegram.Error
	if errors.As(err, &te) && strings.Contains(te.Description, "message is not modified") {
		return nil // the same button pressed twice
	}
	return err
}

func (a *Adapter) reply(ctx context.Context, chat string, r bot.Reply) error {
	text, kb := render(r)
	_, err := a.c.SendMessage(ctx, chat, text, kb, false)
	return err
}

func userOf(u telegram.User) bot.User {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = u.Username
	}
	return bot.User{ID: strconv.FormatInt(u.ID, 10), Name: name}
}

// render turns a reply into HTML and buttons: one row per item, with each
// menu's action on it ("⏯ Pause X", "🗑 Delete X").
func render(r bot.Reply) (string, *telegram.Keyboard) {
	text := telegram.HTML(bot.Truncate(r.Text, maxReply))
	var rows [][]telegram.Button
	for i := 0; len(rows) < maxButtons; i++ {
		var row []telegram.Button
		for _, m := range r.Menus {
			if i >= len(m.Options) {
				continue
			}
			o := m.Options[i]
			row = append(row, telegram.Button{
				Text:         icons[m.Action] + bot.Truncate(o.Label, 40),
				CallbackData: m.Action + ":" + o.Value, // short: an action and an ID
			})
		}
		if len(row) == 0 {
			break
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return text, nil
	}
	return text, &telegram.Keyboard{InlineKeyboard: rows}
}

var icons = map[string]string{bot.ActionToggle: "⏯ ", bot.ActionDelete: "🗑 ", bot.ActionLink: "🔗 "}

// menu lists the commands for Telegram's command menu.
func menu(cmds []bot.Command) []telegram.BotCommand {
	out := []telegram.BotCommand{{Command: cmdHelp, Description: "What I can do and how to write commands"}}
	for _, c := range cmds {
		out = append(out, telegram.BotCommand{Command: c.Name, Description: bot.Truncate(c.Description, 256)})
	}
	return out
}
