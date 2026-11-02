package bizprofile

// SwapDefault prepares a change of the default locale from previous to
// in.Default: the business the old default had becomes previous's card, so its
// pages keep their LocalBusiness, and the default business is stripped of what
// only the old default had. Fold then folds the new default's card in.
//
//   - The card gets the business's locale-only fields (name, country name,
//     image, business type), its address block and, unless the business has an
//     office, no_office. Phone, hours and every inheritable field need no copy:
//     Fold copies the old value into the card where the new default's card
//     overrides it, and marks the field not inherited where the old business
//     left it empty.
//   - A card already stored under previous (a leftover) is not overwritten:
//     its own values win and it only gains what it left empty. An address of
//     its own (street or city) keeps its office switch; otherwise the business's
//     address fills the block and the office switch follows the business.
//   - The business loses its country name, image, business type, address block
//     and office switch, so the new default never prints the old default's
//     address or country. It keeps its name (the legal name of the
//     organisation) and every inheritable field. Without a card for the new
//     default the office stays off until the owner gives it an address.
func SwapDefault(in FoldIn, previous string) FoldIn {
	f := in.Site
	card := in.Cards[previous]
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&card.Name, f.Name)
	fill(&card.CountryName, f.CountryName)
	fill(&card.Image, f.Image)
	fill(&card.BusinessType, f.BusinessType)
	if card.Address.Street == "" && card.Address.City == "" {
		card.NoOffice = card.NoOffice || !f.Office
	}
	card.Address = foldAddress(f.Address, card.Address)

	f.CountryName, f.Image, f.BusinessType = "", "", ""
	f.Address = Address{}
	f.Office, f.NoOffice = false, false

	cards := make(map[string]Profile, len(in.Cards)+1)
	for tag, c := range in.Cards {
		cards[tag] = c
	}
	cards[previous] = card
	return FoldIn{Default: in.Default, Site: f, Cards: cards}
}
