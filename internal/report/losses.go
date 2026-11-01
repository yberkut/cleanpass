package report

import (
	"fmt"
	"io"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/text"
)

// WriteLosses names each thing a result would lose. A Loss holds no value.
func WriteLosses(w io.Writer, losses []lossaudit.Loss) error {
	for i := range losses {
		if _, err := fmt.Fprintln(w, "  "+lossLine(&losses[i])); err != nil {
			return err
		}
	}
	return nil
}

func lossLine(loss *lossaudit.Loss) string {
	object := loss.Object.String()
	switch loss.Kind {
	case lossaudit.VersionLost:
		return text.LossVersion(object, formatTime(loss.Time))
	case lossaudit.AttachmentLost:
		return text.LossAttachment(object)
	case lossaudit.DeletionLost:
		return text.LossDeletion(object)
	case lossaudit.GroupLost:
		return text.LossGroup(object)
	case lossaudit.GroupFieldsLost:
		return text.LossGroupFields(object)
	case lossaudit.CustomIconLost:
		return text.LossCustomIcon(object)
	case lossaudit.CustomDataLost:
		return text.LossCustomData(loss.Name)
	case lossaudit.SettingLost:
		return text.LossSetting(loss.Name)
	}
	return object
}
