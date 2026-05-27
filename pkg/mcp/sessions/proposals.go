package sessions

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// DeleteProposal removes a single proposal markdown by id from dir. No-op
// when the file is already gone. Mirrors wiki.DeleteProposal.
func DeleteProposal(dir, proposalID string) error {
	p := filepath.Join(dir, proposalID+".md")
	if err := os.Remove(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}
