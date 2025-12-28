package pulseboard

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Phase 1: Cartesian Product Tests
// =============================================================================

func TestCartesianProduct_TwoDimensions(t *testing.T) {
	dims := map[string][]string{
		"x": {"a", "b"},
		"y": {"1", "2"},
	}

	result := cartesianProduct(dims)

	if len(result) != 4 {
		t.Fatalf("cartesianProduct() returned %d combinations, want 4", len(result))
	}

	// verify sorted key order (x, y) and preserved value order
	expected := []map[string]string{
		{"x": "a", "y": "1"},
		{"x": "a", "y": "2"},
		{"x": "b", "y": "1"},
		{"x": "b", "y": "2"},
	}

	for i, want := range expected {
		if result[i]["x"] != want["x"] || result[i]["y"] != want["y"] {
			t.Errorf("combination[%d] = %v, want %v", i, result[i], want)
		}
	}
}

func TestCartesianProduct_SingleDimension(t *testing.T) {
	dims := map[string][]string{
		"env": {"prod", "staging", "dev"},
	}

	result := cartesianProduct(dims)

	if len(result) != 3 {
		t.Fatalf("cartesianProduct() returned %d combinations, want 3", len(result))
	}

	// verify order preserved
	expected := []string{"prod", "staging", "dev"}
	for i, want := range expected {
		if result[i]["env"] != want {
			t.Errorf("combination[%d][env] = %v, want %v", i, result[i]["env"], want)
		}
	}
}

func TestCartesianProduct_ThreeDimensions(t *testing.T) {
	dims := map[string][]string{
		"a": {"1", "2"},
		"b": {"x", "y"},
		"c": {"p", "q"},
	}

	result := cartesianProduct(dims)

	if len(result) != 8 {
		t.Fatalf("cartesianProduct() returned %d combinations, want 8 (2x2x2)", len(result))
	}

	// verify first combination uses sorted key order (a, b, c)
	first := result[0]
	if first["a"] != "1" || first["b"] != "x" || first["c"] != "p" {
		t.Errorf("first combination = %v, want {a:1, b:x, c:p}", first)
	}
}

func TestCartesianProduct_EmptyDimension(t *testing.T) {
	dims := map[string][]string{
		"x": {},
	}

	result := cartesianProduct(dims)

	if len(result) != 0 {
		t.Errorf("cartesianProduct() with empty dimension returned %d combinations, want 0", len(result))
	}
}

func TestCartesianProduct_EmptyMap(t *testing.T) {
	dims := map[string][]string{}

	result := cartesianProduct(dims)

	if len(result) != 0 {
		t.Errorf("cartesianProduct() with empty map returned %d combinations, want 0", len(result))
	}
}

func TestCartesianProduct_DeterministicOrder(t *testing.T) {
	dims := map[string][]string{
		"z": {"3", "4"},
		"a": {"1", "2"},
	}

	// run 100 times and verify identical output
	var first []map[string]string
	for i := 0; i < 100; i++ {
		result := cartesianProduct(dims)
		if first == nil {
			first = result
			continue
		}

		if len(result) != len(first) {
			t.Fatalf("iteration %d: length changed from %d to %d", i, len(first), len(result))
		}

		for j := range first {
			if result[j]["a"] != first[j]["a"] || result[j]["z"] != first[j]["z"] {
				t.Fatalf("iteration %d: combination[%d] differs: %v vs %v", i, j, result[j], first[j])
			}
		}
	}
}

func TestCartesianProduct_PreservesValueOrder(t *testing.T) {
	// values are NOT in alphabetical order
	dims := map[string][]string{
		"env": {"prod", "staging", "dev"},
	}

	result := cartesianProduct(dims)

	if len(result) != 3 {
		t.Fatalf("cartesianProduct() returned %d combinations, want 3", len(result))
	}

	// should preserve slice order, not sort values
	expected := []string{"prod", "staging", "dev"}
	for i, want := range expected {
		if result[i]["env"] != want {
			t.Errorf("value order not preserved: combination[%d][env] = %v, want %v", i, result[i]["env"], want)
		}
	}
}

// =============================================================================
// Phase 2: Grid Options Tests
// =============================================================================

func TestWithURLTemplate_Valid(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithURLTemplate("https://api.example.com/health?env={{.env}}")

	if err := opt(cfg); err != nil {
		t.Fatalf("WithURLTemplate() error = %v", err)
	}

	if cfg.urlTemplate != "https://api.example.com/health?env={{.env}}" {
		t.Errorf("urlTemplate = %v, want template string", cfg.urlTemplate)
	}
}

func TestWithURLTemplate_Empty(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithURLTemplate("")

	err := opt(cfg)
	if err == nil {
		t.Error("WithURLTemplate(\"\") expected error, got nil")
	}
}

func TestWithDimensions_Valid(t *testing.T) {
	cfg := &gridConfig{}
	dims := map[string][]string{
		"env":    {"prod", "staging"},
		"region": {"us", "eu"},
	}
	opt := WithDimensions(dims)

	if err := opt(cfg); err != nil {
		t.Fatalf("WithDimensions() error = %v", err)
	}

	if len(cfg.dimensions) != 2 {
		t.Errorf("dimensions count = %d, want 2", len(cfg.dimensions))
	}
}

func TestWithDimensions_Empty(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithDimensions(map[string][]string{})

	err := opt(cfg)
	if err == nil {
		t.Error("WithDimensions({}) expected error, got nil")
	}
}

func TestWithDimensions_EmptyValues(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithDimensions(map[string][]string{
		"env": {},
	})

	err := opt(cfg)
	if err == nil {
		t.Error("WithDimensions with empty values expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "env") {
		t.Errorf("error should mention dimension name 'env', got: %v", err)
	}
}

func TestWithDimensions_EmptyStringValue(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithDimensions(map[string][]string{
		"env": {"prod", "", "staging"},
	})

	err := opt(cfg)
	if err == nil {
		t.Error("WithDimensions with empty string value expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "empty value") {
		t.Errorf("error should mention 'empty value', got: %v", err)
	}
}

func TestWithGridLabels_Valid(t *testing.T) {
	cfg := &gridConfig{staticLabels: make(map[string]string)}
	opt := WithGridLabels("team", "platform", "tier", "critical")

	if err := opt(cfg); err != nil {
		t.Fatalf("WithGridLabels() error = %v", err)
	}

	if cfg.staticLabels["team"] != "platform" {
		t.Errorf("staticLabels[team] = %v, want platform", cfg.staticLabels["team"])
	}
	if cfg.staticLabels["tier"] != "critical" {
		t.Errorf("staticLabels[tier] = %v, want critical", cfg.staticLabels["tier"])
	}
}

func TestWithGridLabels_OddArgs(t *testing.T) {
	cfg := &gridConfig{staticLabels: make(map[string]string)}
	opt := WithGridLabels("team", "platform", "orphan")

	err := opt(cfg)
	if err == nil {
		t.Error("WithGridLabels with odd args expected error, got nil")
	}
}

func TestWithGridLabels_Empty(t *testing.T) {
	cfg := &gridConfig{staticLabels: make(map[string]string)}
	opt := WithGridLabels()

	if err := opt(cfg); err != nil {
		t.Errorf("WithGridLabels() with no args should not error, got: %v", err)
	}
}

func TestWithGridHeaders_Valid(t *testing.T) {
	cfg := &gridConfig{headers: make(map[string]string)}
	opt := WithGridHeaders("Authorization", "Bearer token", "X-Custom", "value")

	if err := opt(cfg); err != nil {
		t.Fatalf("WithGridHeaders() error = %v", err)
	}

	if cfg.headers["Authorization"] != "Bearer token" {
		t.Errorf("headers[Authorization] = %v, want 'Bearer token'", cfg.headers["Authorization"])
	}
}

func TestWithGridHeaders_OddArgs(t *testing.T) {
	cfg := &gridConfig{headers: make(map[string]string)}
	opt := WithGridHeaders("Authorization")

	err := opt(cfg)
	if err == nil {
		t.Error("WithGridHeaders with odd args expected error, got nil")
	}
}

func TestWithGridTimeout_Valid(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridTimeout(30 * time.Second)

	if err := opt(cfg); err != nil {
		t.Fatalf("WithGridTimeout() error = %v", err)
	}

	if cfg.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", cfg.timeout)
	}
}

func TestWithGridTimeout_Zero(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridTimeout(0)

	// zero is valid (means use default)
	if err := opt(cfg); err != nil {
		t.Errorf("WithGridTimeout(0) should not error, got: %v", err)
	}
}

func TestWithGridTimeout_Negative(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridTimeout(-1 * time.Second)

	err := opt(cfg)
	if err == nil {
		t.Error("WithGridTimeout with negative value expected error, got nil")
	}
}

func TestWithGridExtractor_Valid(t *testing.T) {
	cfg := &gridConfig{}
	extractor := func(body []byte, statusCode int) Status {
		return StatusUp
	}
	opt := WithGridExtractor(extractor)

	if err := opt(cfg); err != nil {
		t.Fatalf("WithGridExtractor() error = %v", err)
	}

	if cfg.extractor == nil {
		t.Error("extractor should be set")
	}
}

func TestWithGridExtractor_Nil(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridExtractor(nil)

	// nil is valid (uses default)
	if err := opt(cfg); err != nil {
		t.Errorf("WithGridExtractor(nil) should not error, got: %v", err)
	}
}

// =============================================================================
// Phase 3: NewEndpointGrid Core Tests
// =============================================================================

func TestNewEndpointGrid_Basic(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?x={{.x}}&y={{.y}}"),
		WithDimensions(map[string][]string{
			"x": {"a", "b"},
			"y": {"1", "2"},
		}),
	)

	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}

	if len(endpoints) != 4 {
		t.Errorf("NewEndpointGrid() returned %d endpoints, want 4", len(endpoints))
	}

	// verify all names are unique
	names := make(map[string]bool)
	for _, ep := range endpoints {
		if names[ep.Name()] {
			t.Errorf("duplicate endpoint name: %s", ep.Name())
		}
		names[ep.Name()] = true
	}
}

func TestNewEndpointGrid_SingleDimension(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging", "dev"},
		}),
	)

	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}

	if len(endpoints) != 3 {
		t.Errorf("NewEndpointGrid() returned %d endpoints, want 3", len(endpoints))
	}
}

func TestNewEndpointGrid_URLTemplateRendering(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?x={{.x}}&y={{.y}}"),
		WithDimensions(map[string][]string{
			"x": {"a"},
			"y": {"1"},
		}),
	)

	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	want := "https://api.example.com/health?x=a&y=1"
	if endpoints[0].URL() != want {
		t.Errorf("URL() = %v, want %v", endpoints[0].URL(), want)
	}
}

func TestNewEndpointGrid_URLEncoding(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"space", "hello world", "hello+world"},
		{"ampersand", "a&b", "a%26b"},
		{"equals", "a=b", "a%3Db"},
		{"question", "a?b", "a%3Fb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoints, err := NewEndpointGrid("Test",
				WithURLTemplate("https://api.example.com/health?q={{.q}}"),
				WithDimensions(map[string][]string{
					"q": {tt.value},
				}),
			)
			if err != nil {
				t.Fatalf("NewEndpointGrid() error = %v", err)
			}
			if len(endpoints) != 1 {
				t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
			}

			want := "https://api.example.com/health?q=" + tt.expected
			if endpoints[0].URL() != want {
				t.Errorf("URL() = %v, want %v", endpoints[0].URL(), want)
			}
		})
	}
}

func TestNewEndpointGrid_EndpointNaming(t *testing.T) {
	endpoints, err := NewEndpointGrid("Stream Status",
		WithURLTemplate("https://api.example.com/health?channel={{.channel}}&env={{.env}}"),
		WithDimensions(map[string][]string{
			"channel": {"itv1"},
			"env":     {"prod"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	// values ordered by sorted keys in name
	want := "Stream Status (itv1/prod)"
	if endpoints[0].Name() != want {
		t.Errorf("Name() = %v, want %v", endpoints[0].Name(), want)
	}
}

func TestNewEndpointGrid_DimensionLabels(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?x={{.x}}&y={{.y}}"),
		WithDimensions(map[string][]string{
			"x": {"a"},
			"y": {"1"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	labels := endpoints[0].Labels()
	if labels["x"] != "a" {
		t.Errorf("Labels()[x] = %v, want 'a'", labels["x"])
	}
	if labels["y"] != "1" {
		t.Errorf("Labels()[y] = %v, want '1'", labels["y"])
	}
}

func TestNewEndpointGrid_StaticLabels(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
		WithGridLabels("team", "platform"),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	labels := endpoints[0].Labels()
	if labels["team"] != "platform" {
		t.Errorf("Labels()[team] = %v, want 'platform'", labels["team"])
	}
}

func TestNewEndpointGrid_StaticLabelsOverrideDimensions(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
		WithGridLabels("env", "override"),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	labels := endpoints[0].Labels()
	// static labels should override dimension labels
	if labels["env"] != "override" {
		t.Errorf("Labels()[env] = %v, want 'override' (static should win)", labels["env"])
	}
}

func TestNewEndpointGrid_SharedHeaders(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
		WithGridHeaders("Authorization", "Bearer token"),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	for i, ep := range endpoints {
		headers := ep.Headers()
		if headers["Authorization"] != "Bearer token" {
			t.Errorf("endpoint[%d].Headers()[Authorization] = %v, want 'Bearer token'", i, headers["Authorization"])
		}
	}
}

func TestNewEndpointGrid_SharedTimeout(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
		WithGridTimeout(30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	for i, ep := range endpoints {
		if ep.Timeout() != 30*time.Second {
			t.Errorf("endpoint[%d].Timeout() = %v, want 30s", i, ep.Timeout())
		}
	}
}

func TestNewEndpointGrid_SharedExtractor(t *testing.T) {
	customExtractor := func(body []byte, statusCode int) Status {
		return StatusDegraded
	}

	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
		WithGridExtractor(customExtractor),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	for i, ep := range endpoints {
		extractor := ep.Extractor()
		if extractor == nil {
			t.Errorf("endpoint[%d].Extractor() = nil, want non-nil", i)
			continue
		}
		// verify it's our custom extractor
		if extractor(nil, 200) != StatusDegraded {
			t.Errorf("endpoint[%d] has wrong extractor", i)
		}
	}
}

func TestNewEndpointGrid_ReturnsEndpointSlice(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
	)

	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}

	// verify it's []Endpoint (value type), not []*Endpoint
	_ = []Endpoint(endpoints)
	if len(endpoints) != 1 {
		t.Errorf("expected 1 endpoint, got %d", len(endpoints))
	}
}

func TestNewEndpointGrid_ComposableWithExistingAPI(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
	)

	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}

	// should be usable with WithEndpoints
	pb, err := New(WithEndpoints(endpoints...))
	if err != nil {
		t.Fatalf("New(WithEndpoints(...)) error = %v", err)
	}

	if pb == nil {
		t.Error("New() returned nil PulseBoard")
	}
}

func TestNewEndpointGrid_MissingTemplate(t *testing.T) {
	_, err := NewEndpointGrid("Test",
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
	)

	if err == nil {
		t.Error("NewEndpointGrid() without template expected error, got nil")
	}
}

func TestNewEndpointGrid_MissingDimensions(t *testing.T) {
	_, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health"),
	)

	if err == nil {
		t.Error("NewEndpointGrid() without dimensions expected error, got nil")
	}
}

func TestNewEndpointGrid_InvalidTemplateSyntax(t *testing.T) {
	_, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
	)

	if err == nil {
		t.Error("NewEndpointGrid() with invalid template expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "template") {
		t.Errorf("error should mention 'template', got: %v", err)
	}
}

func TestNewEndpointGrid_TemplateMissingKey(t *testing.T) {
	_, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?x={{.missing}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
	)

	if err == nil {
		t.Error("NewEndpointGrid() with missing template key expected error, got nil")
	}
}

func TestNewEndpointGrid_OptionError(t *testing.T) {
	_, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health"),
		WithDimensions(map[string][]string{}), // will error
	)

	if err == nil {
		t.Error("NewEndpointGrid() with failing option expected error, got nil")
	}
}

// =============================================================================
// Phase 4: Edge Cases & Error Handling Tests
// =============================================================================

func TestNewEndpointGrid_TemplateWithConditional(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate(`{{if eq .env "prod"}}https{{else}}http{{end}}://api.example.com/health`),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	// prod should use https
	if !strings.HasPrefix(endpoints[0].URL(), "https://") {
		t.Errorf("prod endpoint URL should start with https://, got: %s", endpoints[0].URL())
	}

	// staging should use http
	if !strings.HasPrefix(endpoints[1].URL(), "http://") || strings.HasPrefix(endpoints[1].URL(), "https://") {
		t.Errorf("staging endpoint URL should start with http://, got: %s", endpoints[1].URL())
	}
}

func TestNewEndpointGrid_DimensionKeyWithDot(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate(`https://api.example.com/health?key={{index . "my.key"}}`),
		WithDimensions(map[string][]string{
			"my.key": {"value"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	// verify URL contains the value
	if !strings.Contains(endpoints[0].URL(), "key=value") {
		t.Errorf("URL should contain 'key=value', got: %s", endpoints[0].URL())
	}
}

func TestNewEndpointGrid_UnicodeValues(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?region={{.region}}"),
		WithDimensions(map[string][]string{
			"region": {"日本", "한국"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	// labels should preserve original unicode
	labels := endpoints[0].Labels()
	if labels["region"] != "日本" {
		t.Errorf("Labels()[region] = %v, want '日本'", labels["region"])
	}
}

func TestNewEndpointGrid_LongValues(t *testing.T) {
	longValue := strings.Repeat("a", 1000)

	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?id={{.id}}"),
		WithDimensions(map[string][]string{
			"id": {longValue},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	// should work without truncation
	labels := endpoints[0].Labels()
	if labels["id"] != longValue {
		t.Errorf("Labels()[id] length = %d, want %d", len(labels["id"]), len(longValue))
	}
}

func TestNewEndpointGrid_SpecialCharsInValues(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/search?q={{.q}}"),
		WithDimensions(map[string][]string{
			"q": {"foo&bar=baz?qux"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	url := endpoints[0].URL()
	// should not contain raw special chars that would break URL
	if strings.Contains(url, "&bar") || strings.Contains(url, "=baz") || strings.Contains(url, "?qux") {
		t.Errorf("URL contains unescaped special chars: %s", url)
	}
}

func TestNewEndpointGrid_EmptyBaseName(t *testing.T) {
	_, err := NewEndpointGrid("",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
	)

	// empty base name should cause error (from NewEndpoint validation)
	if err == nil {
		t.Error("NewEndpointGrid() with empty base name expected error, got nil")
	}
}

func TestNewEndpointGrid_WhitespaceBaseName(t *testing.T) {
	_, err := NewEndpointGrid("   ",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
	)

	// whitespace-only name should cause error
	if err == nil {
		t.Error("NewEndpointGrid() with whitespace base name expected error, got nil")
	}
}

func TestNewEndpointGrid_SingleValueSingleDimension(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"only"},
		}),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
}

func TestNewEndpointGrid_LargeDimensions(t *testing.T) {
	// 10 x 10 x 10 = 1000 endpoints
	vals := make([]string, 10)
	for i := 0; i < 10; i++ {
		vals[i] = string(rune('0' + i))
	}

	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?a={{.a}}&b={{.b}}&c={{.c}}"),
		WithDimensions(map[string][]string{
			"a": vals,
			"b": vals,
			"c": vals,
		}),
	)

	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}

	if len(endpoints) != 1000 {
		t.Errorf("expected 1000 endpoints, got %d", len(endpoints))
	}

	// verify no duplicate names
	names := make(map[string]bool)
	for _, ep := range endpoints {
		if names[ep.Name()] {
			t.Errorf("duplicate endpoint name: %s", ep.Name())
		}
		names[ep.Name()] = true
	}
}

func TestNewEndpointGrid_SharedMethod(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
		WithGridMethod("POST"),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	for i, ep := range endpoints {
		if ep.Method() != "POST" {
			t.Errorf("endpoint[%d].Method() = %v, want POST", i, ep.Method())
		}
	}
}

func TestWithGridMethod_Invalid(t *testing.T) {
	_, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
		WithGridMethod("PUT"),
	)
	if err == nil {
		t.Error("NewEndpointGrid() expected error for invalid method, got nil")
	}
}

// =============================================================================
// Grid Interval Tests
// =============================================================================

func TestWithGridInterval_Valid(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(30 * time.Second)

	if err := opt(cfg); err != nil {
		t.Fatalf("WithGridInterval() error = %v", err)
	}

	if cfg.interval != 30*time.Second {
		t.Errorf("interval = %v, want 30s", cfg.interval)
	}
}

func TestWithGridInterval_Zero(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(0)

	// zero is valid (means use global)
	if err := opt(cfg); err != nil {
		t.Errorf("WithGridInterval(0) should not error, got: %v", err)
	}
}

func TestWithGridInterval_Minimum(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(time.Second)

	if err := opt(cfg); err != nil {
		t.Errorf("WithGridInterval(1s) should not error, got: %v", err)
	}

	if cfg.interval != time.Second {
		t.Errorf("interval = %v, want 1s", cfg.interval)
	}
}

func TestWithGridInterval_Maximum(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(time.Hour)

	if err := opt(cfg); err != nil {
		t.Errorf("WithGridInterval(1h) should not error, got: %v", err)
	}

	if cfg.interval != time.Hour {
		t.Errorf("interval = %v, want 1h", cfg.interval)
	}
}

func TestWithGridInterval_TooShort(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(500 * time.Millisecond)

	err := opt(cfg)
	if err == nil {
		t.Error("WithGridInterval(500ms) expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "at least 1 second") {
		t.Errorf("error should mention 'at least 1 second', got: %v", err)
	}
}

func TestWithGridInterval_TooLong(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(2 * time.Hour)

	err := opt(cfg)
	if err == nil {
		t.Error("WithGridInterval(2h) expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "exceed 1 hour") {
		t.Errorf("error should mention 'exceed 1 hour', got: %v", err)
	}
}

func TestWithGridInterval_Negative(t *testing.T) {
	cfg := &gridConfig{}
	opt := WithGridInterval(-1 * time.Second)

	err := opt(cfg)
	if err == nil {
		t.Error("WithGridInterval(-1s) expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "cannot be negative") {
		t.Errorf("error should mention 'cannot be negative', got: %v", err)
	}
}

func TestNewEndpointGrid_SharedInterval(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod", "staging"},
		}),
		WithGridInterval(45*time.Second),
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	for i, ep := range endpoints {
		if ep.Interval() != 45*time.Second {
			t.Errorf("endpoint[%d].Interval() = %v, want 45s", i, ep.Interval())
		}
	}
}

func TestNewEndpointGrid_NoIntervalUsesDefault(t *testing.T) {
	endpoints, err := NewEndpointGrid("Test",
		WithURLTemplate("https://api.example.com/health?env={{.env}}"),
		WithDimensions(map[string][]string{
			"env": {"prod"},
		}),
		// no WithGridInterval
	)
	if err != nil {
		t.Fatalf("NewEndpointGrid() error = %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}

	// default interval is 0 (use global)
	if endpoints[0].Interval() != 0 {
		t.Errorf("Interval() = %v, want 0 (use global)", endpoints[0].Interval())
	}
}

// =============================================================================
// Phase 5: Benchmarks
// =============================================================================

func BenchmarkCartesianProduct_Small(b *testing.B) {
	dims := map[string][]string{
		"x": {"1", "2", "3"},
		"y": {"a", "b", "c"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cartesianProduct(dims)
	}
}

func BenchmarkCartesianProduct_Medium(b *testing.B) {
	vals := make([]string, 10)
	for i := 0; i < 10; i++ {
		vals[i] = string(rune('0' + i))
	}

	dims := map[string][]string{
		"x": vals,
		"y": vals,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cartesianProduct(dims)
	}
}

func BenchmarkCartesianProduct_Large(b *testing.B) {
	vals := make([]string, 10)
	for i := 0; i < 10; i++ {
		vals[i] = string(rune('0' + i))
	}

	dims := map[string][]string{
		"x": vals,
		"y": vals,
		"z": vals,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cartesianProduct(dims)
	}
}

func BenchmarkNewEndpointGrid_1000Endpoints(b *testing.B) {
	vals := make([]string, 10)
	for i := 0; i < 10; i++ {
		vals[i] = string(rune('0' + i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewEndpointGrid("Test",
			WithURLTemplate("https://api.example.com/health?a={{.a}}&b={{.b}}&c={{.c}}"),
			WithDimensions(map[string][]string{
				"a": vals,
				"b": vals,
				"c": vals,
			}),
		)
	}
}

func BenchmarkNewEndpointGrid_Memory(b *testing.B) {
	vals := make([]string, 10)
	for i := 0; i < 10; i++ {
		vals[i] = string(rune('0' + i))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewEndpointGrid("Test",
			WithURLTemplate("https://api.example.com/health?a={{.a}}&b={{.b}}&c={{.c}}"),
			WithDimensions(map[string][]string{
				"a": vals,
				"b": vals,
				"c": vals,
			}),
		)
	}
}
