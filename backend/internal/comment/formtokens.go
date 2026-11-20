package comment

import "context"

// PurgeFormTokens deletes used form tokens whose expiry passed and returns how
// many went. A purged id can never be replayed: the issuer refuses a token
// older than its max age, and expires_at is issue time plus that max age.
func (s *Service) PurgeFormTokens(ctx context.Context) (int64, error) {
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comment_form_tokens WHERE expires_at < $1`, s.utcNow())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
