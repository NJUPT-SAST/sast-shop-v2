package service

import (
	"database/sql"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/support"
)

func observeExec(_ sql.Result, err error) { support.ObserveError(err) }
