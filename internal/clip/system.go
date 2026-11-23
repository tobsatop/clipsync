package clip

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"golang.design/x/clipboard"
)

// System is the real clipboard of this computer.
type System struct{}

func NewSystem() (*System, error) {
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	return &System{}, nil
}

func (*System) Seq() int64 { return changeCount() }

func (*System) Read(ctx context.Context) (Item, error) {
	// If the password check itself fails, skip this copy rather than risk
	// sending a password.
	secret, err := clipboard.Sensitive(ctx)
	if err != nil {
		return Item{}, fmt.Errorf("check for password-manager marker: %w", err)
	}
	if secret {
		return Item{}, ErrSensitive
	}
	b, err := clipboard.Read(ctx, clipboard.FmtText)
	if errors.Is(err, clipboard.ErrNoData) || (err == nil && len(b) == 0) {
		return Item{}, ErrNothing
	}
	if err != nil {
		return Item{}, err
	}
	return Item{Kind: Text, Data: NormalizeText(b)}, nil
}

func (*System) Write(ctx context.Context, it Item) error {
	switch it.Kind {
	case Text:
		b := it.Data
		if runtime.GOOS == "windows" {
			b = toCRLF(b)
		}
		_, err := clipboard.Write(ctx, clipboard.FmtText, b)
		return err
	}
	return fmt.Errorf("cannot put %s on the clipboard yet", it.Kind)
}
