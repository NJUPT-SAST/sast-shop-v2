package westpocket

import (
	"database/sql"
	"io"

	"github.com/rs/zerolog/log"
)

func observeError(err error) {
	if err != nil {
		log.Warn().Str("code", safeError(err)).Msg("West Pocket background operation failed")
	}
}
func observeExec(_ sql.Result, err error) { observeError(err) }
func closeResource(resource io.Closer)    { observeError(resource.Close()) }

// 带边界保护的int转int32辅助函数，防止截断成负数，但是直接用math.MaxInt32就可以
// boundedInt32 is used for already constrained image dimensions, counts and pagination.
func boundedInt32(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > 2147483647 {
		return 2147483647
	}
	return int32(v)
}
