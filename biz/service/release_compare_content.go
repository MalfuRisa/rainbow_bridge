package service

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// newCompareNonce prevents literal user text from colliding with an internal
// managed-asset token for this request.
func newCompareNonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// compareAssetToken encodes the semantic identity of a managed resource.
// Snapshot-local asset IDs are deliberately excluded.
func compareAssetToken(nonce string, asset model.ConfigReleaseAsset, suffix string) string {
	payload, _ := json.Marshal([]any{
		"asset", nonce, asset.Sha256, strconv.FormatInt(asset.Size, 10),
		asset.ContentType, asset.FileName, suffix,
	})
	return "asset:" + base64.RawURLEncoding.EncodeToString(payload)
}

func compareLiteralToken(value string) string {
	return "lit:" + strconv.Itoa(len(value)) + ":" + value
}

// canonicalReleaseContent builds a comparison-only representation. It never
// rewrites the stored payload and never exposes this form to clients.
func canonicalReleaseContent(content, typ string, assets map[string]model.ConfigReleaseAsset,
	nonce string) (string, error) {
	resolve := func(assetID, suffix string) (string, bool) {
		asset, ok := assets[assetID]
		if !ok {
			return "", false
		}
		return compareAssetToken(nonce, asset, suffix), true
	}
	switch normalizeConfigTypeString(typ) {
	case "object", "keyvalue":
		var value any
		if err := DecodeConfigJSON([]byte(content), &value); err != nil {
			return "", releaseIncompleteError("release config content is not valid JSON")
		}
		canonical, err := canonicalJSONValue(value, resolve)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(canonical)
		if err != nil {
			return "", releaseInternalError(err)
		}
		return "json:" + string(encoded), nil
	case "richtext":
		rendered, err := renderReleaseContentWith(content, typ, resolve)
		if err != nil {
			return "", releaseIncompleteError("release richtext cannot be normalized")
		}
		return "rt:" + rendered, nil
	default:
		if assetID, suffix, matched := matchReleaseMarker(content); matched {
			if token, ok := resolve(assetID, suffix); ok {
				return token, nil
			}
			return "", releaseIncompleteError("release asset reference has no manifest row")
		}
		return "text:" + compareLiteralToken(content), nil
	}
}

func canonicalJSONValue(value any, resolve func(string, string) (string, bool)) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			next, err := canonicalJSONValue(item, resolve)
			if err != nil {
				return nil, err
			}
			result[key] = next
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			next, err := canonicalJSONValue(item, resolve)
			if err != nil {
				return nil, err
			}
			result[i] = next
		}
		return result, nil
	case string:
		if assetID, suffix, matched := matchReleaseMarker(typed); matched {
			if token, ok := resolve(assetID, suffix); ok {
				return token, nil
			}
			return nil, releaseIncompleteError("release asset reference has no manifest row")
		}
		return compareLiteralToken(typed), nil
	case json.Number:
		return "num:" + typed.String(), nil
	case bool:
		if typed {
			return "bool:true", nil
		}
		return "bool:false", nil
	case nil:
		return "null:", nil
	default:
		return compareLiteralToken(toStringValue(typed)), nil
	}
}

func toStringValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
