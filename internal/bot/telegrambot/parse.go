package telegrambot

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/A1nar-S/advertisiment-monitoring/internal/bot"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
)

// Commands only Telegram has: it opens every chat with /start.
const (
	cmdStart = "start"
	cmdHelp  = "help"
)

// Parsed is a command message read against the bot's commands.
type Parsed struct {
	Call bot.Call
	// Path is how the command is written: "/watch cars".
	Path string
}

// ParseError is a message for the user: what is wrong and how to write it.
type ParseError struct {
	text string
	// missing names a required param left out; complete says the bot can
	// suggest values for it.
	missing  string
	complete bool
}

func (e *ParseError) Error() string { return e.text }

// keyRe finds where "key=" options start.
var keyRe = regexp.MustCompile(`(?:^|\s)([A-Za-z_]+)=`)

// Parse reads "/watch cars name=Cheap Skoda make=Skoda, Toyota price_max=7000".
// A value runs until the next key=, so it may hold spaces and commas. A
// command with one option also takes it bare: "/unwatch Cheap Skoda". chat
// is the chat it came from, the target of a required channel option.
func Parse(cmds map[string]bot.Command, text, chat string) (Parsed, *ParseError) {
	head, rest := cutSpace(strings.TrimSpace(text))
	name := strings.ToLower(strings.TrimPrefix(head, "/"))
	name, _, _ = strings.Cut(name, "@") // "/watch@my_bot" in a group
	rest = strings.TrimSpace(rest)
	p := Parsed{Call: bot.Call{Command: name, Args: map[string]any{}}, Path: "/" + name}
	if name == cmdStart || name == cmdHelp {
		return p, nil
	}
	spec, ok := cmds[name]
	if !ok {
		return p, &ParseError{text: fmt.Sprintf("I don't know /%s. Send /help to see my commands.", name)}
	}
	if len(spec.Subcommands) > 0 {
		var subName string
		subName, rest = cutSpace(rest)
		i := slices.IndexFunc(spec.Subcommands, func(c bot.Command) bool { return strings.EqualFold(c.Name, subName) })
		if i < 0 {
			return p, &ParseError{text: commandUsage(spec)}
		}
		spec = spec.Subcommands[i]
		p.Call.Sub, p.Path = spec.Name, p.Path+" "+spec.Name
	}

	if err := parseArgs(&p, spec, rest); err != nil {
		return p, err
	}
	for _, param := range spec.Params {
		if param.Kind == bot.Channel && param.Required {
			// Setup's status channel: this chat, the only one the bot is in here.
			p.Call.Args[param.Name] = chat
			p.Call.Postable = map[string]bool{chat: true}
			continue
		}
		if _, given := p.Call.Args[param.Name]; param.Required && !given {
			return p, &ParseError{
				text:    fmt.Sprintf("❌ %s needs %s.\n\n%s", p.Path, param.Name, usage(p.Path, spec)),
				missing: param.Name, complete: param.Complete,
			}
		}
	}
	return p, nil
}

func parseArgs(p *Parsed, spec bot.Command, rest string) *ParseError {
	if rest == "" {
		return nil
	}
	locs := keyRe.FindAllStringSubmatchIndex(rest, -1)
	if len(locs) == 0 {
		// Bare: the first text option, when that's unambiguous.
		i := slices.IndexFunc(spec.Params, func(p bot.Param) bool { return p.Kind == bot.Text })
		if i < 0 {
			return &ParseError{text: fmt.Sprintf("%s takes no options.", p.Path)}
		}
		return setArg(p, spec, spec.Params[i].Name, rest)
	}
	if lead := strings.TrimSpace(rest[:locs[0][0]]); lead != "" {
		return &ParseError{text: fmt.Sprintf("❌ Write options as key=value; what is %q?\n\n%s", lead, usage(p.Path, spec))}
	}
	for i, loc := range locs {
		end := len(rest)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if err := setArg(p, spec, strings.ToLower(rest[loc[2]:loc[3]]), rest[loc[1]:end]); err != nil {
			return err
		}
	}
	return nil
}

func setArg(p *Parsed, spec bot.Command, key, value string) *ParseError {
	value = unquote(strings.TrimSpace(value))
	i := slices.IndexFunc(spec.Params, func(p bot.Param) bool { return p.Name == key })
	if i < 0 {
		return &ParseError{text: fmt.Sprintf("❌ %s has no option %q.\n\n%s", p.Path, key, usage(p.Path, spec))}
	}
	param := spec.Params[i]
	switch {
	case param.Kind == bot.Channel:
		return &ParseError{text: "❌ Here I post in this chat only; leave channel out."}
	case param.MaxLen > 0 && len([]rune(value)) > param.MaxLen:
		return &ParseError{text: fmt.Sprintf("❌ %s is too long: at most %d characters.", key, param.MaxLen)}
	case len(param.Choices) > 0 && value != "":
		// Forgive case and diacritics: "pardod" is "Pārdod".
		j := slices.IndexFunc(param.Choices, func(c string) bool { return textutil.Fold(c) == textutil.Fold(value) })
		if j < 0 {
			return &ParseError{text: fmt.Sprintf("❌ %s is one of: %s.", key, strings.Join(param.Choices, ", "))}
		}
		value = param.Choices[j]
	}
	if value != "" {
		p.Call.Args[key] = value
	}
	return nil
}

// cutSpace splits off the first word; a line break separates too.
func cutSpace(s string) (word, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// unquote drops one pair of quotes around a value: "Joe's cars" stays.
func unquote(s string) string {
	for _, q := range [][2]string{{`"`, `"`}, {"'", "'"}, {"“", "”"}} {
		if len(s) >= len(q[0])+len(q[1]) && strings.HasPrefix(s, q[0]) && strings.HasSuffix(s, q[1]) {
			return strings.TrimSpace(s[len(q[0]) : len(s)-len(q[1])])
		}
	}
	return s
}

// Help lists the commands.
func Help(cmds []bot.Command) string {
	var sb strings.Builder
	sb.WriteString("👋 I tell you about new ads that match your searches.\n\n**Commands**\n")
	for _, c := range cmds {
		fmt.Fprintf(&sb, "/%s — %s\n", c.Name, escape(c.Description))
	}
	sb.WriteString("\nSend a command without options to see how to write it, e.g. /watch cars.")
	return sb.String()
}

// commandUsage lists a command's subcommands.
func commandUsage(c bot.Command) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**/%s** — %s\n", c.Name, escape(c.Description))
	for _, s := range c.Subcommands {
		fmt.Fprintf(&sb, "• /%s %s — %s\n", c.Name, s.Name, escape(s.Description))
	}
	sb.WriteString("Send one of them to see its options.")
	return sb.String()
}

// usage explains a command's options.
func usage(path string, c bot.Command) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** — %s", path, escape(c.Description))
	if c.Example != "" {
		cmd, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), " ")
		fmt.Fprintf(&sb, "\ne.g. /%s %s", cmd, escape(c.Example))
	}
	var lines []string
	for _, p := range c.Params {
		if p.Kind == bot.Channel {
			continue
		}
		line := "• **" + p.Name + "**"
		if p.Required {
			line += " (required)"
		}
		line += " — " + escape(p.Description)
		if len(p.Choices) > 0 && len(p.Choices) <= 10 {
			line += " (" + escape(strings.Join(p.Choices, ", ")) + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		sb.WriteString("\nOptions, as key=value (lists comma separated):\n" + strings.Join(lines, "\n"))
	}
	return sb.String()
}

// escape keeps command texts from turning into markdown.
var escape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "[", `\[`, "<", `\<`).Replace
