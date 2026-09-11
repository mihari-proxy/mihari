package app

import "context"

type serviceSlotEntry struct {
	State, Identity string
	Present         bool
}
type serviceObjectSlots interface {
	Read(context.Context, string) (serviceSlotEntry, error)
	Publish(context.Context, string) error
	Exchange(context.Context, string) error
	Retain(context.Context, string) error
}

func publishServiceObject(ctx context.Context, slots serviceObjectSlots, versions []serviceObjectVersion, live serviceSlotEntry, want, recordedBoot, currentBoot string) error {
	if live.Present && !serviceObjectMatches(versions, live.State, live.Identity, recordedBoot, currentBoot) {
		return unknownInstallState()
	}
	if (live.Present && live.State == want) || (!live.Present && want == "absent") {
		return nil
	}
	for _, v := range versions {
		entry, err := slots.Read(ctx, v.Name)
		if err != nil {
			return err
		}
		if entry.Present && !serviceObjectMatches(versions, entry.State, entry.Identity, recordedBoot, currentBoot) {
			return unknownInstallState()
		}
		if want == "absent" {
			if !entry.Present {
				return slots.Retain(ctx, v.Name)
			}
			continue
		}
		if !entry.Present || entry.State != want {
			continue
		}
		if live.Present {
			return slots.Exchange(ctx, v.Name)
		}
		return slots.Publish(ctx, v.Name)
	}
	return unknownInstallState()
}
