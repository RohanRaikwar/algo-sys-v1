package indengine

import (
	"context"
	"log"
)

// peekLoop subscribes to 1s candle PubSub for live indicator previews.
func (svc *Service) peekLoop(ctx context.Context) {
	if err := svc.redisReader.Subscribe1sForPeek(ctx, svc.cfg.EnabledTFs, svc.tfCandleCh); err != nil {
		log.Printf("[indengine] 1s peek subscription error: %v", err)
	}
}
