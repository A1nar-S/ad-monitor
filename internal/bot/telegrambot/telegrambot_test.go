package telegrambot

import (
	"reflect"
	"strings"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/bot"
)

// cmds is a slice of the bot's commands, enough to parse against.
var cmds = map[string]bot.Command{
	bot.CmdWatch: {Name: bot.CmdWatch, Description: "Get alerts", Subcommands: []bot.Command{{
		Name: "cars", Description: "Watch cars", Example: "cars name=Cheap make=Skoda", Params: []bot.Param{
			{Name: "name", Kind: bot.Text, Required: true, MaxLen: 20},
			{Name: "deal", Kind: bot.Text, Choices: []string{"Pārdod", "Pērk"}},
			{Name: "make", Kind: bot.Text, Complete: true},
			{Name: "price_max", Kind: bot.Number},
			{Name: "channel", Kind: bot.Channel},
		},
	}}},
	bot.CmdUnwatch: {Name: bot.CmdUnwatch, Description: "Delete", Params: []bot.Param{
		{Name: "name", Kind: bot.Text, Required: true, Complete: true},
	}},
	bot.CmdSearches: {Name: bot.CmdSearches, Description: "List"},
	bot.CmdSetup: {Name: bot.CmdSetup, Subcommands: []bot.Command{{
		Name: bot.SetupStatusChannel, Params: []bot.Param{{Name: "channel", Kind: bot.Channel, Required: true}},
	}}},
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bot.Call
	}{
		{"/watch cars name=Cheap Skoda make=Skoda, Toyota price_max=7000",
			bot.Call{Command: "watch", Sub: "cars", Args: map[string]any{"name": "Cheap Skoda", "make": "Skoda, Toyota", "price_max": "7000"}}},
		{"/Watch CARS name=\"x y\"  deal=pardod", bot.Call{Command: "watch", Sub: "cars", Args: map[string]any{"name": "x y", "deal": "Pārdod"}}},
		{"/watch@my_bot cars name=a", bot.Call{Command: "watch", Sub: "cars", Args: map[string]any{"name": "a"}}},
		{"/unwatch Cheap Skoda", bot.Call{Command: "unwatch", Args: map[string]any{"name": "Cheap Skoda"}}},
		{"/unwatch Joe's cars'", bot.Call{Command: "unwatch", Args: map[string]any{"name": "Joe's cars'"}}},
		{"/watch\ncars\nname=a\nmake=Skoda", bot.Call{Command: "watch", Sub: "cars", Args: map[string]any{"name": "a", "make": "Skoda"}}},
		{"/searches", bot.Call{Command: "searches", Args: map[string]any{}}},
		{"/start", bot.Call{Command: "start", Args: map[string]any{}}},
		{"/setup status-channel", bot.Call{Command: "setup", Sub: "status-channel", Args: map[string]any{"channel": "42"},
			Postable: map[string]bool{"42": true}}},
	} {
		p, err := Parse(cmds, tc.text, "42")
		if err != nil {
			t.Errorf("%q: %v", tc.text, err)
			continue
		}
		if !reflect.DeepEqual(p.Call, tc.want) {
			t.Errorf("%q:\n got %+v\nwant %+v", tc.text, p.Call, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"/nope", "I don't know /nope"},
		{"/watch", "/watch cars — Watch cars"},
		{"/watch boats name=x", "Send one of them"},
		{"/watch cars make=Skoda", "/watch cars needs name"},
		{"/watch cars name=x colour=red", `has no option "colour"`},
		{"/watch cars Skoda name=x", `what is "Skoda"?`},
		{"/watch cars name=x deal=swap", "deal is one of: Pārdod, Pērk."},
		{"/watch cars name=x channel=5", "this chat only"},
		{"/watch cars name=" + strings.Repeat("x", 21), "at most 20 characters"},
		{"/searches all", "/searches takes no options"},
	} {
		_, err := Parse(cmds, tc.text, "42")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.text, err, tc.want)
		}
	}
	// A missing search name says so, for the adapter to list the user's.
	if _, err := Parse(cmds, "/unwatch", "42"); err == nil || err.missing != "name" || !err.complete {
		t.Errorf("unwatch without a name: %+v", err)
	}
}

func TestUsage(t *testing.T) {
	got := usage("/watch cars", cmds[bot.CmdWatch].Subcommands[0])
	for _, want := range []string{"e.g. /watch cars name=Cheap make=Skoda", "• **name** (required)", "(Pārdod, Pērk)"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "channel") {
		t.Errorf("usage offers channel, which Telegram can't use:\n%s", got)
	}
}

func TestRender(t *testing.T) {
	text, kb := render(bot.Reply{Text: "**Your searches**", Menus: []bot.Menu{
		{Action: bot.ActionToggle, Options: []bot.MenuOption{{Label: "Pause A", Value: "1"}, {Label: "Resume B", Value: "2"}}},
		{Action: bot.ActionDelete, Options: []bot.MenuOption{{Label: "Delete A", Value: "1"}, {Label: "Delete B", Value: "2"}}},
	}})
	if text != "<b>Your searches</b>" || kb == nil || len(kb.InlineKeyboard) != 2 {
		t.Fatalf("render = %q, %+v", text, kb)
	}
	row := kb.InlineKeyboard[1]
	if len(row) != 2 || row[0].Text != "⏯ Resume B" || row[0].CallbackData != "toggle:2" || row[1].CallbackData != "delete:2" {
		t.Errorf("row of B = %+v", row)
	}
	if _, kb := render(bot.Reply{Text: "x"}); kb != nil {
		t.Errorf("no menus, but buttons: %+v", kb)
	}
}
