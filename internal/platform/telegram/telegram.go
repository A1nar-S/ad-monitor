// Package telegram is a small client for the Telegram Bot API: the few
// methods the bot and the notifier use, over plain HTTPS.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultBase = "https://api.telegram.org"

// maxRetryAfter is the longest rate-limit pause waited out in place; a
// longer one is returned as an error for the caller's retry.
const maxRetryAfter = 30 * time.Second

// Client calls the Bot API with one bot's token.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(token string) *Client {
	// Long polling holds a request open for up to a minute.
	return &Client{base: defaultBase, token: token, http: &http.Client{Timeout: 90 * time.Second}}
}

// WithBaseURL points the client at another server (tests).
func (c *Client) WithBaseURL(base string) *Client {
	c.base = base
	return c
}

// Error is the API refusing a call.
type Error struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Call runs a method with params (JSON) and decodes its result into out
// (nil to ignore). A short rate-limit pause is waited out once.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	err := c.call(ctx, method, params, out)
	var te *Error
	if errors.As(err, &te) && te.Code == http.StatusTooManyRequests && te.RetryAfter <= maxRetryAfter {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(te.RetryAfter):
		}
		err = c.call(ctx, method, params, out)
	}
	return err
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: bad request", method) // the URL holds the token
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error repeats the URL, and with it the token: keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d, unreadable reply: %w", method, resp.StatusCode, err)
	}
	if !r.OK {
		e := &Error{Code: r.ErrorCode, Description: r.Description}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		if r.Parameters != nil {
			e.RetryAfter = time.Duration(r.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

// ChatID passes a numeric chat ID as a number, anything else (@channel) as is.
func ChatID(target string) any {
	if n, err := strconv.ParseInt(target, 10, 64); err == nil {
		return n
	}
	return target
}

// --- types --------------------------------------------------------------------

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // private, group, supergroup, channel
	Title string `json:"title"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type Keyboard struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// --- methods ----------------------------------------------------------------------

type linkPreview struct {
	IsDisabled bool `json:"is_disabled"`
}

// SendMessage sends HTML text. Link previews are off unless preview is set:
// a preview of a login link would spend it.
func (c *Client) SendMessage(ctx context.Context, chat, html string, kb *Keyboard, preview bool) (Message, error) {
	params := map[string]any{
		"chat_id": ChatID(chat), "text": html, "parse_mode": "HTML",
		"link_preview_options": linkPreview{IsDisabled: !preview},
	}
	if kb != nil {
		params["reply_markup"] = kb
	}
	var m Message
	err := c.Call(ctx, "sendMessage", params, &m)
	return m, err
}

// SendPhoto sends a photo by URL with an HTML caption (up to 1024 characters).
func (c *Client) SendPhoto(ctx context.Context, chat, photoURL, caption string) error {
	return c.Call(ctx, "sendPhoto", map[string]any{
		"chat_id": ChatID(chat), "photo": photoURL, "caption": caption, "parse_mode": "HTML",
	}, nil)
}

// EditMessageText replaces a message's text and buttons.
func (c *Client) EditMessageText(ctx context.Context, chat int64, messageID int64, html string, kb *Keyboard) error {
	params := map[string]any{
		"chat_id": chat, "message_id": messageID, "text": html, "parse_mode": "HTML",
		"link_preview_options": linkPreview{IsDisabled: true},
	}
	if kb != nil {
		params["reply_markup"] = kb
	}
	return c.Call(ctx, "editMessageText", params, nil)
}

// AnswerCallbackQuery stops a pressed button's spinner.
func (c *Client) AnswerCallbackQuery(ctx context.Context, id string) error {
	return c.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id}, nil)
}

// GetUpdates long-polls for new messages and button presses.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var us []Update
	err := c.Call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}, &us)
	return us, err
}

func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.Call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.Call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}
