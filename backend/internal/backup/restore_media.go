package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/media"
)

// checkObjectMedia is the media check of a restore that involves object
// storage: the archive carries a media manifest, the target is object storage,
// or both.
//
//   - bytes in the archive, store set: the prefix must be empty (refusal).
//   - manifest only, store set: the objects are compared with the bucket; what
//     is missing or differs is reported as a warning (read-only, it does not
//     stop the restore: the database is restorable either way).
//   - manifest only, no store: a warning that the bytes are not here.
func (x *restorer) checkObjectMedia(ctx context.Context, plan *RestorePlan, refuse *[]string) error {
	s := x.scan
	switch {
	case x.o.MediaStore != nil && s.mediaFiles > 0:
		lister, ok := x.o.MediaStore.(media.Lister)
		if !ok {
			return errors.New("cannot check the media prefix: the media store cannot list objects")
		}
		objs, err := lister.List(ctx, "")
		if err != nil {
			return fmt.Errorf("cannot list the media object storage: %w", err)
		}
		if len(objs) > 0 {
			*refuse = append(*refuse, fmt.Sprintf("the media object storage prefix is not empty (%d object%s)", len(objs), plural(len(objs))))
		} else {
			plan.Checks = append(plan.Checks, "media object storage empty")
		}
	case x.o.MediaStore != nil && s.mediaObjects != nil:
		res := verifyMediaManifest(ctx, x.o.MediaStore, s.mediaObjects)
		switch {
		case res.Unverified != nil:
			plan.Warnings = append(plan.Warnings, "media objects could not be checked against object storage: "+res.Unverified.Error())
		case len(res.Missing) == 0 && len(res.Mismatched) == 0:
			plan.Checks = append(plan.Checks, fmt.Sprintf("media objects ok (%d)", res.Total))
		default:
			plan.Warnings = append(plan.Warnings, mediaProblemLine(res.Total, "missing from object storage", res.Missing)...)
			plan.Warnings = append(plan.Warnings, mediaProblemLine(res.Total, "differ from the backup (size or sha256)", res.Mismatched)...)
		}
	case x.o.MediaStore == nil && s.mediaObjects != nil && s.mediaFiles == 0:
		n := len(s.mediaObjects.Files)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("the backup lists %d media object%s in object storage and holds no media bytes; set MEDIA_STORAGE=s3 to check them", n, plural(n)))
	}
	return nil
}

// mediaProblemLine words one kind of problem, naming the first few keys.
func mediaProblemLine(total int, what string, keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	sample := keys[:min(len(keys), maxMediaProblemsListed)]
	line := fmt.Sprintf("%d of %d media object%s %s: %s", len(keys), total, plural(total), what, strings.Join(sample, ", "))
	if len(keys) > len(sample) {
		line += fmt.Sprintf(" and %d more", len(keys)-len(sample))
	}
	return []string{line}
}
