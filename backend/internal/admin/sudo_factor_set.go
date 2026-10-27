package admin

// SetSecondFactor registers the provider sudo mode asks for a code. Pass nil
// to remove it. It is safe to call while the admin serves requests.
func (a *Admin) SetSecondFactor(f SecondFactor) {
	a.sudo.mu.Lock()
	a.sudo.factor = f
	a.sudo.mu.Unlock()
}
