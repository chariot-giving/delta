package deltariver

import (
	"strconv"

	"github.com/riverqueue/river/rivertype"
)

type riverJob struct {
	row *rivertype.JobRow
}

func (j *riverJob) ID() string {
	return strconv.FormatInt(j.row.ID, 10)
}

func (j *riverJob) Kind() string {
	return j.row.Kind
}
