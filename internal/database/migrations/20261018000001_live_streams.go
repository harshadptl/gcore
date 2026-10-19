package migrations

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

func init() {
	type liveStream struct {
		bun.BaseModel `bun:"table:live_streams"`
		UserID        int64     `bun:",pk"`
		GcoreStreamID int64     `bun:"gcore_stream_id,notnull"`
		Title         string    `bun:",notnull,default:''"`
		PushURL       string    `bun:"push_url,type:text,notnull,default:''"`
		PushURLSRT    string    `bun:"push_url_srt,type:text,notnull,default:''"`
		PushURLWHIP   string    `bun:"push_url_whip,type:text,notnull,default:''"`
		IframeURL     string    `bun:"iframe_url,type:text,notnull,default:''"`
		Active        bool      `bun:",notnull,default:false"`
		Live          bool      `bun:",notnull,default:false"`
		ArmedAt       time.Time `bun:",nullzero"`
		StartedAt     time.Time `bun:",nullzero"`
		EndedAt       time.Time `bun:",nullzero"`
		SyncedAt      time.Time `bun:",nullzero,notnull,default:current_timestamp"`
		CreatedAt     time.Time `bun:",nullzero,notnull,default:current_timestamp"`
		UpdatedAt     time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	}

	up := func(ctx context.Context, db *bun.DB) error {
		_, err := db.NewCreateTable().Model((*liveStream)(nil)).
			ForeignKey(`("user_id") REFERENCES "users" ("id") ON DELETE CASCADE`).
			Exec(ctx)
		return err
	}

	down := func(ctx context.Context, db *bun.DB) error {
		_, err := db.NewDropTable().Model((*liveStream)(nil)).IfExists().Exec(ctx)
		return err
	}

	Migrations.MustRegister(up, down)
}
