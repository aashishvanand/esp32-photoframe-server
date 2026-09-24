package service

// Shared building blocks for the DB-backed photo sources (gallery, immich,
// synology, google_photos). Each per-source plugin owns its own DB filter
// and image loader; everything else — random selection with exclusion
// fallback, orientation-aware smart collage, photo-date lookup — lives here
// so the plugins stay small and uniform.

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/aitjcize/esp32-photoframe-server/backend/internal/imagesource"
	"github.com/aitjcize/esp32-photoframe-server/backend/internal/model"
	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/imageops"
)

// PhotoPicker selects one DB photo record matching the given orientation
// (empty string = any) and excluding the given IDs. Different sources use
// different filter clauses, but the contract is the same.
type PhotoPicker func(orientationFilter string, excludeIDs []uint) (model.Image, error)

// PhotoLoader decodes the underlying photo bytes for a DB record into an
// image. Synology / Immich call out to their services; gallery and Google
// Photos read from local files.
type PhotoLoader func(item model.Image) (image.Image, error)

// RunDBPhotoFlow is the shared workflow every DB-backed photo source uses:
// optionally compose a smart collage when the photo orientation mismatches
// the device, otherwise pick + load one photo, then look up PhotoTakenAt
// if the device shows photo dates. The two callbacks (pick / load) are
// the per-source bits.
func RunDBPhotoFlow(
	req *imagesource.Request,
	db *gorm.DB,
	pick PhotoPicker,
	load PhotoLoader,
) (*imagesource.Response, error) {
	var img image.Image
	var ids []uint
	var err error

	if req.Device != nil && req.Device.EnableCollage {
		img, ids, err = smartCollage(req.Orientation, req.Width, req.Height, req.ExcludeIDs, pick, load)
	} else {
		var item model.Image
		item, img, err = pickAndLoad(pick, load, req.Orientation, req.ExcludeIDs, nil)
		if err == nil {
			ids = []uint{item.ID}
		}
	}
	if err != nil {
		return nil, err
	}

	resp := &imagesource.Response{Image: img, ImageIDs: ids}
	if req.Device != nil && req.Device.ShowPhotoDate && len(ids) > 0 && ids[0] != 0 {
		var stored model.Image
		if e := db.Select("photo_taken_at").First(&stored, ids[0]).Error; e == nil {
			resp.PhotoTakenAt = stored.PhotoTakenAt
		}
	}
	return resp, nil
}

// PickRandomDBPhoto returns a random model.Image for the given source,
// optionally filtered by orientation ("landscape" / "portrait" — "auto" is
// always matched alongside) and excluding ids. Generic over the four sources
// that follow the source = ? filter shape (gallery, immich, synology, google).
func PickRandomDBPhoto(db *gorm.DB, source, orientationFilter string, excludeIDs []uint) (model.Image, error) {
	query := db.Order("RANDOM()").Where("source = ?", source)
	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}
	if orientationFilter != "" {
		query = query.Where("orientation IN ?", []string{orientationFilter, "auto"})
	}
	var item model.Image
	err := query.First(&item).Error
	return item, err
}

// maxPhotoLoadAttempts bounds how many different photos one request tries
// to load before giving up, so a single broken or unreachable photo (deleted
// upstream, NAS 404, corrupt file) doesn't fail the whole refresh — issue #61.
const maxPhotoLoadAttempts = 3

// pickRandomWithFallback prefers the device orientation, then relaxes the
// exclusion list, then relaxes the orientation — so a portrait device draws
// portrait photos but still shows something when its library is small or has
// no matching-orientation photos. Pass orientation="" (e.g. the collage path,
// which composes across orientations) for the original any-orientation pick.
// failedIDs are photos that already failed to load in this request; unlike
// excludeIDs they are never relaxed.
func pickRandomWithFallback(pick PhotoPicker, orientation string, excludeIDs, failedIDs []uint) (model.Image, error) {
	// 1. Device orientation, excluding recently shown photos.
	item, err := pick(orientation, append(append([]uint(nil), excludeIDs...), failedIDs...))
	if err == nil {
		return item, nil
	}
	// 2. Same orientation, but allow recently shown photos (small library).
	if len(excludeIDs) > 0 {
		if item, err = pick(orientation, failedIDs); err == nil {
			return item, nil
		}
	}
	// 3. No photo matches the orientation at all — fall back to any.
	if orientation != "" {
		if item, err = pick("", failedIDs); err == nil {
			return item, nil
		}
	}
	return item, err
}

// pickAndLoad picks a photo with pickRandomWithFallback and loads it. When a
// load fails, that photo is appended to *failedIDs and another one is picked,
// up to maxPhotoLoadAttempts. If the pool runs dry after a load failure, the
// load error is returned rather than the picker's "record not found", since
// photos do exist — they just can't be loaded.
func pickAndLoad(
	pick PhotoPicker,
	load PhotoLoader,
	orientation string,
	excludeIDs []uint,
	failedIDs *[]uint,
) (model.Image, image.Image, error) {
	if failedIDs == nil {
		failedIDs = new([]uint)
	}
	var lastErr error
	for attempt := 1; attempt <= maxPhotoLoadAttempts; attempt++ {
		item, err := pickRandomWithFallback(pick, orientation, excludeIDs, *failedIDs)
		if err != nil {
			if lastErr != nil {
				return model.Image{}, nil, fmt.Errorf("%d photo(s) failed to load and no other photo is available: %w", len(*failedIDs), lastErr)
			}
			return model.Image{}, nil, err
		}
		img, err := load(item)
		if err == nil {
			return item, img, nil
		}
		logPhotoLoadFailure(item, attempt, err)
		*failedIDs = append(*failedIDs, item.ID)
		lastErr = err
	}
	return model.Image{}, nil, fmt.Errorf("%d photos in a row failed to load: %w", maxPhotoLoadAttempts, lastErr)
}

func logPhotoLoadFailure(item model.Image, attempt int, err error) {
	log.Printf("photo %d (source %s, external id %q) failed to load (attempt %d/%d): %v",
		item.ID, item.Source, item.ExternalID, attempt, maxPhotoLoadAttempts, err)
}

// smartCollage fetches one or two photos and composes them into a collage
// when the first photo's orientation doesn't match the device's. The
// callbacks let each source pick / load through its own backend.
func smartCollage(
	orientation string,
	screenW, screenH int,
	excludeIDs []uint,
	pick PhotoPicker,
	load PhotoLoader,
) (image.Image, []uint, error) {
	// Prefer the explicit device orientation; fall back to the aspect ratio.
	devicePortrait := screenH > screenW
	if orientation == "portrait" {
		devicePortrait = true
	} else if orientation == "landscape" {
		devicePortrait = false
	}

	// The collage composes across orientations, so the first pick stays
	// orientation-agnostic — its shape decides single vs. collage.
	var failedIDs []uint
	item1, img1, err := pickAndLoad(pick, load, "", excludeIDs, &failedIDs)
	if err != nil {
		return nil, nil, err
	}
	servedIDs := []uint{item1.ID}

	bounds := img1.Bounds()
	isPhotoPortrait := bounds.Dy() > bounds.Dx()
	if isPhotoPortrait == devicePortrait {
		return img1, servedIDs, nil
	}

	// Each collage slot has the *opposite* shape of the device: a
	// portrait device stacks two landscape-shaped slots vertically, a
	// landscape device places two portrait-shaped slots side-by-side.
	// We only reach this branch when the first photo's orientation
	// differs from the device's, so the first photo already matches the
	// slot shape — request a second photo of the same orientation.
	targetType := "landscape"
	if isPhotoPortrait {
		targetType = "portrait"
	}

	// A second photo that fails to load is skipped and another one tried;
	// photos that already failed stay excluded even without history.
	var img2 image.Image
	for attempt := 1; attempt <= maxPhotoLoadAttempts && img2 == nil; attempt++ {
		skip := append([]uint{item1.ID}, failedIDs...)
		// 1. Exclude history + the first photo.
		item2, err := pick(targetType, append(append([]uint(nil), excludeIDs...), skip...))
		if err != nil || item2.ID == item1.ID {
			log.Printf("smartCollage: %s query with history exclusion failed: %v, retrying without history", targetType, err)
			// 2. Just exclude the first photo, ignore history.
			item2, err = pick(targetType, skip)
		}
		if err != nil || item2.ID == item1.ID {
			break
		}
		if img2, err = load(item2); err != nil {
			logPhotoLoadFailure(item2, attempt, err)
			failedIDs = append(failedIDs, item2.ID)
			img2 = nil
			continue
		}
		servedIDs = append(servedIDs, item2.ID)
	}
	if img2 == nil {
		log.Printf("smartCollage: no different %s photo found, using same photo twice", targetType)
		img2 = img1
		servedIDs = append(servedIDs, item1.ID)
	}

	if devicePortrait {
		return createVerticalCollage(img1, img2, screenW, screenH), servedIDs, nil
	}
	return createHorizontalCollage(img1, img2, screenW, screenH), servedIDs, nil
}

func createVerticalCollage(img1, img2 image.Image, width, height int) image.Image {
	slotHeight := height / 2
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	imageops.DrawCover(dst, image.Rect(0, 0, width, slotHeight), img1)
	imageops.DrawCover(dst, image.Rect(0, slotHeight, width, height), img2)
	return dst
}

func createHorizontalCollage(img1, img2 image.Image, width, height int) image.Image {
	slotWidth := width / 2
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	imageops.DrawCover(dst, image.Rect(0, 0, slotWidth, height), img1)
	imageops.DrawCover(dst, image.Rect(slotWidth, 0, width, height), img2)
	return dst
}

// ResolveLocalPath handles path differences between docker (/data/...) and
// local dev (./data/...). Used by sources that store photos on disk
// (gallery, google_photos). Returns the original path if nothing resolves.
func ResolveLocalPath(dataDir, path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	for _, prefix := range []string{"/data/", "/app/data/"} {
		if strings.HasPrefix(path, prefix) {
			rel := strings.TrimPrefix(path, prefix)
			candidate := filepath.Join(dataDir, rel)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return path
}

// LoadLocalPhoto opens a model.Image record stored on disk and decodes it.
func LoadLocalPhoto(dataDir string, item model.Image) (image.Image, error) {
	resolved := ResolveLocalPath(dataDir, item.FilePath)
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
