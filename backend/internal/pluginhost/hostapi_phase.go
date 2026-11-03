package pluginhost

import (
	"context"

	"hatamoto.dev/sdk"
)

// phaseSet is the set of run phases a host function may be called in.
type phaseSet uint8

const (
	phNone  phaseSet = 0
	phFetch phaseSet = 1 << iota
	phTransform
	phBoth = phFetch | phTransform
)

func (p phaseSet) allows(phase string) bool {
	switch phase {
	case sdk.RunPhaseFetch:
		return p&phFetch != 0
	case sdk.RunPhaseTransform:
		return p&phTransform != 0
	}
	return false
}

// fnPhases classifies every host function by the run phase it may be called
// in. Inside a run a function missing here is denied (default deny), and a
// test fails when a registered function has no entry.
//
//   - fetch collects data from the outside: network, the owner's upload, the
//     buffer it fills. It sees no site data.
//   - transform turns the buffer into staged content. It reads the published
//     site but cannot reach the network.
//   - plugin storage (kv, db), mail and private form data are closed in both.
var fnPhases = map[string]phaseSet{
	"permissions":     phBoth,
	sdk.FnLog:         phBoth,
	sdk.FnSettingsGet: phBoth,

	sdk.FnRunInfo:       phBoth,
	sdk.FnRunCheckpoint: phBoth,
	sdk.FnRunProgress:   phBoth,
	sdk.FnRunFinish:     phBoth,
	sdk.FnRunFail:       phBoth,

	sdk.FnHTTPFetch:         phFetch,
	sdk.FnUploadRead:        phFetch,
	sdk.FnBufferPut:         phFetch,
	sdk.FnMediaImportURL:    phFetch,
	sdk.FnBufferGet:         phTransform,
	sdk.FnBufferList:        phTransform,
	sdk.FnSiteInfo:          phTransform,
	sdk.FnEntitiesSlugTaken: phTransform,

	sdk.FnEntitiesGet:          phTransform,
	sdk.FnEntitiesList:         phTransform,
	sdk.FnEntitiesTranslations: phTransform,
	sdk.FnTermsList:            phTransform,
	sdk.FnTermsGet:             phTransform,
	sdk.FnMenusGet:             phTransform,
	sdk.FnMenusList:            phTransform,
	sdk.FnMediaGet:             phTransform,
	sdk.FnMediaList:            phTransform,
	sdk.FnRedirectsList:        phTransform,

	sdk.FnChangesetEntityUpsert:   phTransform,
	sdk.FnChangesetTermUpsert:     phTransform,
	sdk.FnChangesetPersonUpsert:   phTransform,
	sdk.FnChangesetMediaAttach:    phTransform,
	sdk.FnChangesetMenuUpsert:     phTransform,
	sdk.FnChangesetRedirectUpsert: phTransform,
	sdk.FnChangesetDelete:         phTransform,
	sdk.FnChangesetPropose:        phTransform,

	sdk.FnKVGet:            phNone,
	sdk.FnKVSet:            phNone,
	sdk.FnKVDelete:         phNone,
	sdk.FnKVList:           phNone,
	sdk.FnDBQuery:          phNone,
	sdk.FnDBExec:           phNone,
	sdk.FnMailSend:         phNone,
	sdk.FnFormsList:        phNone,
	sdk.FnFormsSubmissions: phNone,
}

// runOnly are the functions that exist only inside a run. They carry no
// permission: the run context is their gate.
var runOnly = map[string]bool{
	sdk.FnRunInfo: true, sdk.FnRunCheckpoint: true, sdk.FnRunProgress: true, sdk.FnRunFinish: true, sdk.FnRunFail: true,
	sdk.FnUploadRead: true, sdk.FnBufferPut: true, sdk.FnBufferGet: true, sdk.FnBufferList: true,
	sdk.FnMediaImportURL: true, sdk.FnSiteInfo: true, sdk.FnEntitiesSlugTaken: true,
	sdk.FnChangesetEntityUpsert: true, sdk.FnChangesetTermUpsert: true, sdk.FnChangesetPersonUpsert: true,
	sdk.FnChangesetMediaAttach: true, sdk.FnChangesetMenuUpsert: true, sdk.FnChangesetRedirectUpsert: true,
	sdk.FnChangesetDelete: true, sdk.FnChangesetPropose: true,
}

// removedFns are the direct write functions SDK 0.17 removed, with the
// changeset function that replaces each. Calling one is `unavailable`, not
// `unknown_function`, so a plugin built for an older SDK sees why.
var removedFns = map[string]string{
	"entities.create":     sdk.FnChangesetEntityUpsert,
	"entities.update":     sdk.FnChangesetEntityUpsert,
	"entities.set_status": sdk.FnChangesetEntityUpsert,
	"entities.delete":     sdk.FnChangesetDelete,
	"terms.save":          sdk.FnChangesetTermUpsert,
	"terms.delete":        sdk.FnChangesetDelete,
	"menus.save":          sdk.FnChangesetMenuUpsert,
	"menus.add_item":      sdk.FnChangesetMenuUpsert,
	"menus.delete_item":   sdk.FnChangesetMenuUpsert,
	"media.upload":        sdk.FnMediaImportURL,
	"redirects.create":    sdk.FnChangesetRedirectUpsert,
	"redirects.delete":    sdk.FnChangesetDelete,
}

// phaseGate is the single run/phase check every host call passes. Outside a
// run, run-only functions are refused and everything else behaves as it
// always did. Inside a run only the functions classified for the current
// phase are allowed.
func phaseGate(ctx context.Context, name string) error {
	rc, inRun := RunFrom(ctx)
	if !inRun {
		if runOnly[name] {
			return herr(sdk.ErrPhaseDenied, "%s is only available inside a run", name)
		}
		return nil
	}
	if !fnPhases[name].allows(rc.Phase) {
		return herr(sdk.ErrPhaseDenied, "%s is not available in the %s phase", name, rc.Phase)
	}
	return nil
}
