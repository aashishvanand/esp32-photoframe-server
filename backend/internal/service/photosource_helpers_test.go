package service

import (
	"errors"
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/aitjcize/esp32-photoframe-server/backend/internal/imagesource"
	"github.com/aitjcize/esp32-photoframe-server/backend/internal/model"
)

func mkOrientedImage(t *testing.T, db *gorm.DB, ext, orientation string) model.Image {
	t.Helper()
	img := model.Image{
		ExternalID:  ext,
		Source:      model.SourceImmich,
		Status:      "pending",
		Orientation: orientation,
	}
	if err := db.Create(&img).Error; err != nil {
		t.Fatalf("create image: %v", err)
	}
	return img
}

func dbPicker(db *gorm.DB) PhotoPicker {
	return func(orientation string, exclude []uint) (model.Image, error) {
		return PickRandomDBPhoto(db, model.SourceImmich, orientation, exclude)
	}
}

func stubLoader(model.Image) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 10, 10)), nil
}

type pickCall struct {
	orientation string
	excludeLen  int
}

// The requested orientation must reach the picker on the first attempt.
func TestPickRandomWithFallback_PassesOrientation(t *testing.T) {
	var seen []pickCall
	pick := func(orientation string, exclude []uint) (model.Image, error) {
		seen = append(seen, pickCall{orientation, len(exclude)})
		return model.Image{ID: 1, Orientation: orientation}, nil
	}
	item, err := pickRandomWithFallback(pick, "portrait", []uint{2, 3}, nil)
	assert.NoError(t, err)
	assert.Equal(t, "portrait", item.Orientation)
	assert.Equal(t, []pickCall{{"portrait", 2}}, seen)
}

// An empty orientation+exclusions result retries the *same* orientation without
// exclusions before ever relaxing the orientation.
func TestPickRandomWithFallback_DropsExclusionsBeforeOrientation(t *testing.T) {
	var seen []pickCall
	pick := func(orientation string, exclude []uint) (model.Image, error) {
		seen = append(seen, pickCall{orientation, len(exclude)})
		if orientation == "portrait" && len(exclude) == 0 {
			return model.Image{ID: 5, Orientation: "portrait"}, nil
		}
		return model.Image{}, gorm.ErrRecordNotFound
	}
	item, err := pickRandomWithFallback(pick, "portrait", []uint{1, 2}, nil)
	assert.NoError(t, err)
	assert.Equal(t, uint(5), item.ID)
	assert.Equal(t, "portrait", item.Orientation)
	assert.Equal(t, []pickCall{{"portrait", 2}, {"portrait", 0}}, seen)
}

// Only when no photo of the requested orientation exists does it fall back to
// any orientation.
func TestPickRandomWithFallback_FallsBackToAnyOrientation(t *testing.T) {
	var seen []pickCall
	pick := func(orientation string, exclude []uint) (model.Image, error) {
		seen = append(seen, pickCall{orientation, len(exclude)})
		if orientation == "" {
			return model.Image{ID: 9, Orientation: "landscape"}, nil
		}
		return model.Image{}, gorm.ErrRecordNotFound
	}
	item, err := pickRandomWithFallback(pick, "portrait", []uint{1}, nil)
	assert.NoError(t, err)
	assert.Equal(t, uint(9), item.ID)
	// portrait+exclude → portrait+none → any(none).
	assert.Equal(t, []pickCall{{"portrait", 1}, {"portrait", 0}, {"", 0}}, seen)
}

// End-to-end via RunDBPhotoFlow: a portrait device is never served a landscape
// photo while portrait photos exist.
func TestRunDBPhotoFlow_PrefersDeviceOrientation(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "p1", "portrait")
	mkOrientedImage(t, db, "p2", "portrait")
	mkOrientedImage(t, db, "l1", "landscape")

	req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800}
	for i := 0; i < 40; i++ {
		resp, err := RunDBPhotoFlow(req, db, dbPicker(db), stubLoader)
		assert.NoError(t, err)
		if assert.Len(t, resp.ImageIDs, 1) {
			var img model.Image
			assert.NoError(t, db.First(&img, resp.ImageIDs[0]).Error)
			assert.Equal(t, "portrait", img.Orientation,
				"portrait device should not be served a landscape photo")
		}
	}
}

// "auto" photos (no EXIF dimensions) match any device orientation.
func TestRunDBPhotoFlow_AutoMatchesAnyOrientation(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "a1", "auto")

	req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800}
	resp, err := RunDBPhotoFlow(req, db, dbPicker(db), stubLoader)
	assert.NoError(t, err)
	assert.Len(t, resp.ImageIDs, 1)
}

// When only mismatched-orientation photos exist, still return one rather than
// failing — a cropped photo beats a blank frame.
func TestRunDBPhotoFlow_FallbackWhenNoOrientationMatch(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "l1", "landscape") // only landscape available

	req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800}
	resp, err := RunDBPhotoFlow(req, db, dbPicker(db), stubLoader)
	assert.NoError(t, err)
	assert.Len(t, resp.ImageIDs, 1)
}

var errBrokenPhoto = errors.New("download returned status: 404")

// failingLoader fails for the photos whose ExternalID is in broken and returns
// a portrait (10x20) image for the rest, recording every load attempt.
func failingLoader(broken map[string]bool, loaded *[]string) PhotoLoader {
	return func(item model.Image) (image.Image, error) {
		*loaded = append(*loaded, item.ExternalID)
		if broken[item.ExternalID] {
			return nil, errBrokenPhoto
		}
		return image.NewRGBA(image.Rect(0, 0, 10, 20)), nil
	}
}

// A photo that fails to load is excluded and another one picked, instead of
// failing the whole request (issue #61).
func TestRunDBPhotoFlow_RetriesAnotherPhotoOnLoadFailure(t *testing.T) {
	var seen [][]uint
	pick := func(orientation string, exclude []uint) (model.Image, error) {
		seen = append(seen, append([]uint(nil), exclude...))
		for _, id := range exclude {
			if id == 1 {
				return model.Image{ID: 2, ExternalID: "good"}, nil
			}
		}
		return model.Image{ID: 1, ExternalID: "bad"}, nil
	}
	var loaded []string
	load := failingLoader(map[string]bool{"bad": true}, &loaded)

	req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800, ExcludeIDs: []uint{7}}
	resp, err := RunDBPhotoFlow(req, setupAlbumDB(t), pick, load)
	assert.NoError(t, err)
	assert.Equal(t, []uint{2}, resp.ImageIDs)
	assert.Equal(t, []string{"bad", "good"}, loaded)
	// The retry keeps the history exclusion and adds the failed photo.
	assert.Equal(t, [][]uint{{7}, {7, 1}}, seen)
}

// A failed photo stays excluded even when the picker relaxes the history
// exclusion, so a small library doesn't re-pick the broken photo.
func TestRunDBPhotoFlow_FailedPhotoStaysExcludedWithoutHistory(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "bad", "portrait")
	good := mkOrientedImage(t, db, "good", "portrait")

	for i := 0; i < 20; i++ {
		var loaded []string
		load := failingLoader(map[string]bool{"bad": true}, &loaded)
		// Excluding the good photo as history forces the relaxed pick.
		req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800, ExcludeIDs: []uint{good.ID}}
		resp, err := RunDBPhotoFlow(req, db, dbPicker(db), load)
		assert.NoError(t, err)
		assert.Equal(t, []uint{good.ID}, resp.ImageIDs)
	}
}

// When every attempt fails, the request gives up after maxPhotoLoadAttempts
// with the load error.
func TestRunDBPhotoFlow_GivesUpAfterMaxAttempts(t *testing.T) {
	db := setupAlbumDB(t)
	broken := map[string]bool{}
	for _, ext := range []string{"b1", "b2", "b3", "b4", "b5"} {
		mkOrientedImage(t, db, ext, "portrait")
		broken[ext] = true
	}
	var loaded []string
	req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800}
	_, err := RunDBPhotoFlow(req, db, dbPicker(db), failingLoader(broken, &loaded))
	assert.ErrorIs(t, err, errBrokenPhoto)
	assert.Contains(t, err.Error(), "3 photos in a row failed to load")
	assert.Len(t, loaded, maxPhotoLoadAttempts)
	// Each attempt tried a different photo.
	assert.Len(t, map[string]bool{loaded[0]: true, loaded[1]: true, loaded[2]: true}, 3)
}

// When the only photos fail to load, the load error is reported rather than
// "record not found" — the library isn't empty, its photos are unreachable.
func TestRunDBPhotoFlow_ReportsLoadErrorWhenPoolRunsDry(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "bad", "portrait")

	var loaded []string
	req := &imagesource.Request{Orientation: "portrait", Width: 480, Height: 800}
	_, err := RunDBPhotoFlow(req, db, dbPicker(db), failingLoader(map[string]bool{"bad": true}, &loaded))
	assert.ErrorIs(t, err, errBrokenPhoto)
	assert.NotErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Equal(t, []string{"bad"}, loaded)
}

// Smart collage retries a slot whose photo fails to load: a landscape device
// with only portrait photos always gets two different, loadable photos.
func TestSmartCollage_RetriesFailedSlot(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "bad", "portrait")
	p1 := mkOrientedImage(t, db, "p1", "portrait")
	p2 := mkOrientedImage(t, db, "p2", "portrait")

	req := &imagesource.Request{
		Orientation: "landscape", Width: 800, Height: 480,
		Device: &model.Device{EnableCollage: true},
	}
	for i := 0; i < 20; i++ {
		var loaded []string
		resp, err := RunDBPhotoFlow(req, db, dbPicker(db), failingLoader(map[string]bool{"bad": true}, &loaded))
		assert.NoError(t, err)
		assert.ElementsMatch(t, []uint{p1.ID, p2.ID}, resp.ImageIDs)
	}
}

// When no second photo can be loaded, the collage falls back to the first
// photo in both slots instead of failing the request.
func TestSmartCollage_FallsBackWhenSlotCannotBeFilled(t *testing.T) {
	db := setupAlbumDB(t)
	mkOrientedImage(t, db, "bad", "portrait")
	good := mkOrientedImage(t, db, "good", "portrait")

	req := &imagesource.Request{
		Orientation: "landscape", Width: 800, Height: 480,
		Device: &model.Device{EnableCollage: true},
	}
	for i := 0; i < 20; i++ {
		var loaded []string
		resp, err := RunDBPhotoFlow(req, db, dbPicker(db), failingLoader(map[string]bool{"bad": true}, &loaded))
		assert.NoError(t, err)
		assert.Equal(t, []uint{good.ID, good.ID}, resp.ImageIDs)
	}
}
