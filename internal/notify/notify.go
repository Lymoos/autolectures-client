package notify

import (
	"github.com/Lymoos/autolectures/client/internal/hub"
	"github.com/Lymoos/autolectures/client/internal/logger"
)

type Notifier struct{ hub *hub.Hub }

func New(h *hub.Hub) *Notifier { return &Notifier{hub: h} }

func (n *Notifier) Notify(event, text string, data map[string]any) {
	logger.Infof("Уведомление", "%s", text)
	if n.hub != nil && n.hub.Connected() {
		n.hub.SendNotify(event, text, data)
	}
}

