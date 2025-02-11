package tuiutil

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
)

const DefaultBlinkSpeed = time.Millisecond * 530

// Internal ID management for text inputs. Necessary for blink integrity when
// multiple text inputs are involved.
var (
	Ascii  bool
	Faint  bool
	lastID int
	idMtx  sync.Mutex
)

// Return the next ID we should use on the TextInputModel.
func nextID() int {
	idMtx.Lock()
	defer idMtx.Unlock()
	lastID++
	return lastID
}

// initialBlinkMsg initializes cursor blinking.
type initialBlinkMsg struct{}

// blinkMsg signals that the cursor should blink. It contains metadata that
// allows us to tell if the blink message is the one we're expecting.
type blinkMsg struct {
	id  int
	tag int
}

// blinkCanceled is sent when a blink operation is canceled.
type blinkCanceled struct{}

// Internal messages for clipboard operations.
type pasteMsg string
type pasteErrMsg struct{ error }

// EchoMode sets the input behavior of the text input field.
type EchoMode int

const (
	// EchoNormal displays text as is. This is the default behavior.
	EchoNormal EchoMode = iota

	// EchoPassword displays the EchoCharacter mask instead of actual
	// characters.  This is commonly used for password fields.
	EchoPassword

	// EchoNone displays nothing as characters are entered. This is commonly
	// seen for password fields on the command line.
	EchoNone

	// EchoOnEdit
)

// blinkCtx manages cursor blinking.
type blinkCtx struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// CursorMode describes the behavior of the cursor.
type CursorMode int

// Available cursor modes.
const (
	CursorBlink CursorMode = iota
	CursorStatic
	CursorHide
)

// String returns a the cursor mode in a human-readable format. This method is
// provisional and for informational purposes only.
func (c CursorMode) String() string {
	return [...]string{
		"blink",
		"static",
		"hidden",
	}[c]
}

// TextInputModel is the Bubble Tea model for this text input element.
type TextInputModel struct {
	Err error

	// General settings.
	Prompt        string
	Placeholder   string
	BlinkSpeed    time.Duration
	EchoMode      EchoMode
	EchoCharacter rune

	// Styles. These will be applied as inline styles.
	//
	// For an introduction to styling with Lip Gloss see:
	// https://github.com/charmbracelet/lipgloss
	PromptStyle      lipgloss.Style
	TextStyle        lipgloss.Style
	BackgroundStyle  lipgloss.Style
	PlaceholderStyle lipgloss.Style
	CursorStyle      lipgloss.Style

	// CharLimit is the maximum amount of characters this input element will
	// accept. If 0 or less, there's no limit.
	CharLimit int

	// Width is the maximum number of characters that can be displayed at once.
	// It essentially treats the text field like a horizontally scrolling
	// Viewport. If 0 or less this setting is ignored.
	Width int

	// The ID of this TextInputModel as it relates to other textinput Models.
	id int

	// The ID of the blink message we're expecting to receive.
	blinkTag int

	// Underlying text value.
	value []rune

	// Focus indicates whether user input Focus should be on this input
	// component. When false, ignore keyboard input and hide the cursor.
	Focus bool

	// Cursor blink state.
	blink bool

	// Cursor position.
	pos int

	// Used to emulate a Viewport when width is set and the content is
	// overflowing.
	Offset      int
	OffsetRight int

	// Used to manage cursor blink
	blinkCtx *blinkCtx

	// cursorMode determines the behavior of the cursor
	cursorMode CursorMode
}

// NewModel creates a new model with default settings.
func NewModel() TextInputModel {
	m := TextInputModel{
		Prompt:           "> ",
		BlinkSpeed:       DefaultBlinkSpeed,
		EchoCharacter:    '*',
		CharLimit:        0,
		PlaceholderStyle: lipgloss.NewStyle(),

		id:         nextID(),
		value:      nil,
		Focus:      false,
		blink:      true,
		pos:        0,
		cursorMode: CursorBlink,

		blinkCtx: &blinkCtx{
			ctx: context.Background(),
		},
	}

	if !Ascii {
		m.PlaceholderStyle = m.PlaceholderStyle.Foreground(lipgloss.Color("240"))
	}

	return m
}

// SetValue sets the value of the text input.
func (m *TextInputModel) SetValue(s string) {
	runes := []rune(s)
	if m.CharLimit > 0 && len(runes) > m.CharLimit {
		m.value = runes[:m.CharLimit]
	} else {
		m.value = runes
	}
	if m.pos == 0 || m.pos > len(m.value) {
		m.setCursor(len(m.value))
	}
	m.handleOverflow()
}

// Value returns the value of the text input.
func (m TextInputModel) Value() string {
	return string(m.value)
}

// Cursor returns the cursor position.
func (m TextInputModel) Cursor() int {
	return m.pos
}

// SetCursor moves the cursor to the given position. If the position is
// out of bounds the cursor will be moved to the start or end accordingly.
func (m *TextInputModel) SetCursor(pos int) {
	m.setCursor(pos)
}

// setCursor moves the cursor to the given position and returns whether or not
// the cursor blink should be reset. If the position is out of bounds the
// cursor will be moved to the start or end accordingly.
func (m *TextInputModel) setCursor(pos int) bool {
	m.pos = Clamp(pos, 0, len(m.value))
	m.handleOverflow()

	// Show the cursor unless it's been explicitly hidden
	m.blink = m.cursorMode == CursorHide

	// Reset cursor blink if necessary
	return m.cursorMode == CursorBlink
}

// CursorStart moves the cursor to the start of the input field.
func (m *TextInputModel) CursorStart() {
	m.cursorStart()
}

// cursorStart moves the cursor to the start of the input field and returns
// whether or not the curosr blink should be reset.
func (m *TextInputModel) cursorStart() bool {
	return m.setCursor(0)
}

// CursorEnd moves the cursor to the end of the input field
func (m *TextInputModel) CursorEnd() {
	m.cursorEnd()
}

// CursorMode returns the model's cursor mode. For available cursor modes, see
// type CursorMode.
func (m TextInputModel) CursorMode() CursorMode {
	return m.cursorMode
}

// SetCursorMode CursorMode sets the model's cursor mode. This method returns a command.
//
// For available cursor modes, see type CursorMode.
func (m *TextInputModel) SetCursorMode(mode CursorMode) tea.Cmd {
	m.cursorMode = mode
	m.blink = m.cursorMode == CursorHide || !m.Focus
	if mode == CursorBlink {
		return Blink
	}
	return nil
}

// cursorEnd moves the cursor to the end of the input field and returns whether
// the cursor should blink should reset.
func (m *TextInputModel) cursorEnd() bool {
	return m.setCursor(len(m.value))
}

// Focused returns the Focus state on the model.
func (m TextInputModel) Focused() bool {
	return m.Focus
}

// FocusCommand sets the Focus state on the model. When the model is in Focus it can
// receive keyboard input and the cursor will be hidden.
func (m *TextInputModel) FocusCommand() tea.Cmd {
	m.Focus = true
	m.blink = m.cursorMode == CursorHide // show the cursor unless we've explicitly hidden it

	if m.cursorMode == CursorBlink && m.Focus {
		return m.blinkCmd()
	}
	return nil
}

// Blur removes the Focus state on the model.  When the model is blurred it can
// not receive keyboard input and the cursor will be hidden.
func (m *TextInputModel) Blur() {
	m.Focus = false
	m.blink = true
}

// Reset sets the input to its default state with no input. Returns whether
// or not the cursor blink should reset.
func (m *TextInputModel) Reset() bool {
	m.value = nil
	return m.setCursor(0)
}

// handle a clipboard paste event, if supported. Returns whether or not the
// cursor blink should reset.
func (m *TextInputModel) handlePaste(v string) bool {
	paste := []rune(v)

	var availSpace int
	if m.CharLimit > 0 {
		availSpace = m.CharLimit - len(m.value)
	}

	// If the char limit's been reached cancel
	if m.CharLimit > 0 && availSpace <= 0 {
		return false
	}

	// If there's not enough space to paste the whole thing cut the pasted
	// runes down so they'll fit
	if m.CharLimit > 0 && availSpace < len(paste) {
		paste = paste[:len(paste)-availSpace]
	}

	// Stuff before and after the cursor
	head := m.value[:m.pos]
	tailSrc := m.value[m.pos:]
	tail := make([]rune, len(tailSrc))
	copy(tail, tailSrc)

	// Insert pasted runes
	for _, r := range paste {
		head = append(head, r)
		m.pos++
		if m.CharLimit > 0 {
			availSpace--
			if availSpace <= 0 {
				break
			}
		}
	}

	// Put it all back together
	m.value = append(head, tail...)

	// Reset blink state if necessary and run overflow checks
	return m.setCursor(m.pos)
}

// If a max width is defined, perform some logic to treat the visible area
// as a horizontally scrolling Viewport.
func (m *TextInputModel) handleOverflow() {
	if m.Width <= 0 || rw.StringWidth(string(m.value)) <= m.Width {
		m.Offset = 0
		m.OffsetRight = len(m.value)
		return
	}

	// Correct right Offset if we've deleted characters
	m.OffsetRight = min(m.OffsetRight, len(m.value))

	if m.pos < m.Offset {
		m.Offset = m.pos

		w := 0
		i := 0
		runes := m.value[m.Offset:]

		for i < len(runes) && w <= m.Width {
			w += rw.RuneWidth(runes[i])
			if w <= m.Width+1 {
				i++
			}
		}

		m.OffsetRight = m.Offset + i
	} else if m.pos >= m.OffsetRight {
		m.OffsetRight = m.pos

		w := 0
		runes := m.value[:m.OffsetRight]
		i := len(runes) - 1

		for i > 0 && w < m.Width {
			w += rw.RuneWidth(runes[i])
			if w <= m.Width {
				i--
			}
		}

		m.Offset = m.OffsetRight - (len(runes) - 1 - i)
	}
}

// deleteBeforeCursor deletes all text before the cursor. Returns whether or
// not the cursor blink should be reset.
func (m *TextInputModel) deleteBeforeCursor() bool {
	m.value = m.value[m.pos:]
	m.Offset = 0
	return m.setCursor(0)
}

// deleteAfterCursor deletes all text after the cursor. Returns whether or not
// the cursor blink should be reset. If input is masked delete everything after
// the cursor so as not to reveal word breaks in the masked input.
func (m *TextInputModel) deleteAfterCursor() bool {
	m.value = m.value[:m.pos]
	return m.setCursor(len(m.value))
}

// deleteWordLeft deletes the word left to the cursor. Returns whether or not
// the cursor blink should be reset.
func (m *TextInputModel) deleteWordLeft() bool {
	if m.pos == 0 || len(m.value) == 0 {
		return false
	}

	if m.EchoMode != EchoNormal {
		return m.deleteBeforeCursor()
	}

	i := m.pos
	blink := m.setCursor(m.pos - 1)
	for unicode.IsSpace(m.value[m.pos]) {
		// ignore series of whitespace before cursor
		blink = m.setCursor(m.pos - 1)
	}

	for m.pos > 0 {
		if !unicode.IsSpace(m.value[m.pos]) {
			blink = m.setCursor(m.pos - 1)
		} else {
			if m.pos > 0 {
				// keep the previous space
				blink = m.setCursor(m.pos + 1)
			}
			break
		}
	}

	if i > len(m.value) {
		m.value = m.value[:m.pos]
	} else {
		m.value = append(m.value[:m.pos], m.value[i:]...)
	}

	return blink
}

// deleteWordRight deletes the word right to the cursor. Returns whether or not
// the cursor blink should be reset. If input is masked delete everything after
// the cursor so as not to reveal word breaks in the masked input.
func (m *TextInputModel) deleteWordRight() bool {
	if m.pos >= len(m.value) || len(m.value) == 0 {
		return false
	}

	if m.EchoMode != EchoNormal {
		return m.deleteAfterCursor()
	}

	i := m.pos
	m.setCursor(m.pos + 1)
	for unicode.IsSpace(m.value[m.pos]) {
		// ignore series of whitespace after cursor
		m.setCursor(m.pos + 1)
	}

	for m.pos < len(m.value) {
		if !unicode.IsSpace(m.value[m.pos]) {
			m.setCursor(m.pos + 1)
		} else {
			break
		}
	}

	if m.pos > len(m.value) {
		m.value = m.value[:i]
	} else {
		m.value = append(m.value[:i], m.value[m.pos:]...)
	}

	return m.setCursor(i)
}

// wordLeft moves the cursor one word to the left. Returns whether or not the
// cursor blink should be reset. If input is masked, move input to the start
// so as not to reveal word breaks in the masked input.
func (m *TextInputModel) wordLeft() bool {
	if m.pos == 0 || len(m.value) == 0 {
		return false
	}

	if m.EchoMode != EchoNormal {
		return m.cursorStart()
	}

	blink := false
	i := m.pos - 1
	for i >= 0 {
		if unicode.IsSpace(m.value[i]) {
			blink = m.setCursor(m.pos - 1)
			i--
		} else {
			break
		}
	}

	for i >= 0 {
		if !unicode.IsSpace(m.value[i]) {
			blink = m.setCursor(m.pos - 1)
			i--
		} else {
			break
		}
	}

	return blink
}

// wordRight moves the cursor one word to the right. Returns whether or not the
// cursor blink should be reset. If the input is masked, move input to the end
// so as not to reveal word breaks in the masked input.
func (m *TextInputModel) wordRight() bool {
	if m.pos >= len(m.value) || len(m.value) == 0 {
		return false
	}

	if m.EchoMode != EchoNormal {
		return m.cursorEnd()
	}

	blink := false
	i := m.pos
	for i < len(m.value) {
		if unicode.IsSpace(m.value[i]) {
			blink = m.setCursor(m.pos + 1)
			i++
		} else {
			break
		}
	}

	for i < len(m.value) {
		if !unicode.IsSpace(m.value[i]) {
			blink = m.setCursor(m.pos + 1)
			i++
		} else {
			break
		}
	}

	return blink
}

func (m TextInputModel) echoTransform(v string) string {
	switch m.EchoMode {
	case EchoPassword:
		return strings.Repeat(string(m.EchoCharacter), rw.StringWidth(v))
	case EchoNone:
		return ""

	default:
		return v
	}
}

// Update is the Bubble Tea update loop.
func (m TextInputModel) Update(msg tea.Msg) (TextInputModel, tea.Cmd) {
	if !m.Focus {
		m.blink = true
		return m, nil
	}

	var resetBlink bool

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyBackspace: // delete character before cursor
			if msg.Alt {
				resetBlink = m.deleteWordLeft()
			} else {
				if len(m.value) > 0 {
					m.value = append(m.value[:max(0, m.pos-1)], m.value[m.pos:]...)
					if m.pos > 0 {
						resetBlink = m.setCursor(m.pos - 1)
					}
				}
			}
		case tea.KeyLeft, tea.KeyCtrlB:
			if msg.Alt { // alt+left arrow, back one word
				resetBlink = m.wordLeft()
				break
			}
			if m.pos > 0 { // left arrow, ^F, back one character
				resetBlink = m.setCursor(m.pos - 1)
			}
		case tea.KeyRight, tea.KeyCtrlF:
			if msg.Alt { // alt+right arrow, forward one word
				resetBlink = m.wordRight()
				break
			}
			if m.pos < len(m.value) { // right arrow, ^F, forward one character
				resetBlink = m.setCursor(m.pos + 1)
			}
		case tea.KeyCtrlW: // ^W, delete word left of cursor
			resetBlink = m.deleteWordLeft()
		case tea.KeyHome, tea.KeyCtrlA: // ^A, go to beginning
			resetBlink = m.cursorStart()
		case tea.KeyDelete, tea.KeyCtrlD: // ^D, delete char under cursor
			if len(m.value) > 0 && m.pos < len(m.value) {
				m.value = append(m.value[:m.pos], m.value[m.pos+1:]...)
			}
		case tea.KeyCtrlE, tea.KeyEnd: // ^E, go to end
			resetBlink = m.cursorEnd()
		case tea.KeyCtrlK: // ^K, kill text after cursor
			resetBlink = m.deleteAfterCursor()
		case tea.KeyCtrlU: // ^U, kill text before cursor
			resetBlink = m.deleteBeforeCursor()
		case tea.KeyCtrlV: // ^V paste
			return m, Paste
		case tea.KeyRunes: // input regular characters
			if msg.Alt && len(msg.Runes) == 1 {
				if msg.Runes[0] == 'd' { // alt+d, delete word right of cursor
					resetBlink = m.deleteWordRight()
					break
				}
				if msg.Runes[0] == 'b' { // alt+b, back one word
					resetBlink = m.wordLeft()
					break
				}
				if msg.Runes[0] == 'f' { // alt+f, forward one word
					resetBlink = m.wordRight()
					break
				}
			}

			// Input a regular character
			if m.CharLimit <= 0 || len(m.value) < m.CharLimit {
				m.value = append(m.value[:m.pos], append(msg.Runes, m.value[m.pos:]...)...)
				resetBlink = m.setCursor(m.pos + len(msg.Runes))
			}
		}

	case initialBlinkMsg:
		// We accept all initialBlinkMsgs genrated by the Blink command.

		if m.cursorMode != CursorBlink || !m.Focus {
			return m, nil
		}

		cmd := m.blinkCmd()
		return m, cmd

	case blinkMsg:
		// We're choosy about whether to accept blinkMsgs so that our cursor
		// only exactly when it should.

		// Is this model blinkable?
		if m.cursorMode != CursorBlink || !m.Focus {
			return m, nil
		}

		// Were we expecting this blink message?
		if msg.id != m.id || msg.tag != m.blinkTag {
			return m, nil
		}

		var cmd tea.Cmd
		if m.cursorMode == CursorBlink {
			m.blink = !m.blink
			cmd = m.blinkCmd()
		}
		return m, cmd

	case blinkCanceled: // no-op
		return m, nil

	case pasteMsg:
		resetBlink = m.handlePaste(string(msg))

	case pasteErrMsg:
		m.Err = msg
	}

	var cmd tea.Cmd
	if resetBlink {
		cmd = m.blinkCmd()
	}

	m.handleOverflow()
	return m, cmd
}

// View renders the textinput in its current state.
func (m TextInputModel) View() string {
	// Placeholder text
	if len(m.value) == 0 && m.Placeholder != "" {
		return m.placeholderView()
	}

	styleText := m.TextStyle.Inline(true).Render

	value := m.value[m.Offset:m.OffsetRight]
	pos := max(0, m.pos-m.Offset)
	v := styleText(m.echoTransform(string(value[:pos])))

	if pos < len(value) {
		if Ascii {
			v += "¦"
		}
		v += m.cursorView(m.echoTransform(string(value[pos]))) // cursor and text under it
		v += styleText(m.echoTransform(string(value[pos+1:]))) // text after cursor
	} else {
		v += m.cursorView(" ")
	}

	// If a max width and background color were set fill the empty spaces with
	// the background color.
	valWidth := rw.StringWidth(string(value))
	if m.Width > 0 && valWidth <= m.Width {
		padding := max(0, m.Width-valWidth)
		if valWidth+padding <= m.Width && pos < len(value) {
			padding++
		}
		v += styleText(strings.Repeat(" ", padding))
	}

	return m.PromptStyle.Render(m.Prompt) + v
}

// placeholderView returns the prompt and placeholder view, if any.
func (m TextInputModel) placeholderView() string {
	var (
		v     string
		p     = m.Placeholder
		style = m.PlaceholderStyle.Inline(true).Render
	)

	// Cursor
	if m.blink {
		v += m.cursorView(style(p[:1]))
	} else {
		v += m.cursorView(p[:1])
	}

	// The rest of the placeholder text
	v += style(p[1:])

	return m.PromptStyle.Render(m.Prompt) + v
}

// cursorView styles the cursor.
func (m TextInputModel) cursorView(v string) string {
	if m.blink {
		return m.TextStyle.Render(v)
	}
	s := m.CursorStyle.Inline(true)
	if !Ascii {
		s = s.Reverse(true)
	}

	return s.Render(v)
}

// blinkCmd is an internal command used to manage cursor blinking.
func (m *TextInputModel) blinkCmd() tea.Cmd {
	if m.cursorMode != CursorBlink {
		return nil
	}

	if m.blinkCtx != nil && m.blinkCtx.cancel != nil {
		m.blinkCtx.cancel()
	}

	ctx, cancel := context.WithTimeout(m.blinkCtx.ctx, m.BlinkSpeed)
	m.blinkCtx.cancel = cancel

	m.blinkTag++

	return func() tea.Msg {
		defer cancel()
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			return blinkMsg{id: m.id, tag: m.blinkTag}
		}
		return blinkCanceled{}
	}
}

// Blink is a command used to initialize cursor blinking.
func Blink() tea.Msg {
	return initialBlinkMsg{}
}

// Paste is a command for pasting from the clipboard into the text input.
func Paste() tea.Msg {
	str, err := clipboard.ReadAll()
	if err != nil {
		return pasteErrMsg{err}
	}
	return pasteMsg(str)
}

func Clamp(v, low, high int) int {
	return min(high, max(low, v))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
