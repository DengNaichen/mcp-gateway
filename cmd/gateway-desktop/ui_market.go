package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type marketIconEntry struct {
	loading bool
	failed  bool
	img     ui.ImageSource
	svg     *ui.SVG
}

type marketPageState struct {
	loaded        bool
	loading       bool
	query         string
	registryQuery string
	filter        string // "all" | "http" | "stdio" | "installed"
	items         []apitypes.MarketServer
	registryErr   string
	fetchErr      string

	installingID string
	showModal    bool
	modalItem    *apitypes.MarketServer
	modalInputs  map[string]string
	modalErr     string

	iconMu sync.Mutex
	icons  map[string]*marketIconEntry
}

func (u *DesktopUI) marketState() *marketPageState {
	if u.market == nil {
		u.market = &marketPageState{
			filter:      "all",
			modalInputs: map[string]string{},
			icons:       map[string]*marketIconEntry{},
		}
	}
	return u.market
}

func (u *DesktopUI) renderMarketContent(c *ui.Context) {
	state := u.State
	st := u.marketState()

	if !st.loaded && !st.loading && u.Client != nil {
		u.fetchMarket(st, "")
	}

	installedByID := make(map[string]bool)
	if state != nil {
		for _, s := range state.Registry.Servers {
			if !s.Demo {
				installedByID[s.ID] = true
			}
		}
	}

	// Search & Filter Bar.
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
		inp := SailTextInput(c.Key("mkt-search"), &st.query).
			Placeholder(i18n.T("market.search.placeholder")).
			Grow(1).
			MinWidth(0)
		if inp.Changed() && strings.TrimSpace(st.query) == "" && st.registryQuery != "" {
			u.fetchMarket(st, "")
		}
		if inp.Submitted() {
			u.fetchMarket(st, strings.TrimSpace(st.query))
		}

		regBtnLabel := i18n.T("market.search.registry")
		if st.loading && st.registryQuery != "" {
			regBtnLabel = i18n.T("common.searching")
		}
		if SailButton(c.Key("mkt-search-reg"), regBtnLabel).Disabled(st.loading).Clicked() {
			u.fetchMarket(st, strings.TrimSpace(st.query))
		}
	})

	installedCount := 0
	for _, item := range st.items {
		if item.Installed || installedByID[item.ServerID] {
			installedCount++
		}
	}

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		renderMarketFilterPill(c, st, "all", i18n.T("common.all"))
		renderMarketFilterPill(c, st, "http", i18n.T("market.filter.cloud"))
		renderMarketFilterPill(c, st, "stdio", i18n.T("market.filter.local"))
		renderMarketFilterPill(c, st, "installed", fmt.Sprintf(i18n.T("market.filter.installed"), installedCount))
	})

	if st.fetchErr != "" {
		SailAlertBox(c, ToneDanger, i18n.T("market.error.load"), st.fetchErr)
	}
	if st.registryErr != "" {
		SailAlertBox(c, ToneWarning, i18n.T("market.warning.registry_down"), st.registryErr)
	}

	q := strings.ToLower(strings.TrimSpace(st.query))
	var featuredFiltered []apitypes.MarketServer
	var registryFiltered []apitypes.MarketServer
	for _, item := range st.items {
		item.Installed = item.Installed || installedByID[item.ServerID]
		if !marketMatchesFilter(item, st.filter) {
			continue
		}
		if q != "" && !marketMatchesLocalQuery(item, q) {
			continue
		}
		if item.Featured {
			featuredFiltered = append(featuredFiltered, item)
		} else {
			registryFiltered = append(registryFiltered, item)
		}
	}

	winW, _ := c.Size()
	cols := 3
	if winW > 0 && winW < 1040 {
		cols = 2
	}

	if st.loading && len(st.items) == 0 {
		SailCard(c, func() {
			SailQuietStatus(c, i18n.T("market.loading"), SailWarningInk, SailInkMuted)
		})
	} else if len(featuredFiltered) == 0 && len(registryFiltered) == 0 {
		actionLbl := ""
		var onAction func()
		if strings.TrimSpace(st.query) != "" && strings.TrimSpace(st.query) != st.registryQuery {
			actionLbl = i18n.T("market.search.registry_cta")
			onAction = func() { u.fetchMarket(st, strings.TrimSpace(st.query)) }
		}
		SailEmptyState(c, i18n.T("market.empty.title"), i18n.T("market.empty.subtitle"), actionLbl, onAction)
	} else {
		if len(featuredFiltered) > 0 {
			u.renderMarketGrid(c, st, featuredFiltered, cols, "feat")
		}
		if len(registryFiltered) > 0 {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					ui.Text(c, i18n.T("market.section.registry_results")).FontSize(13).FontWeight(600).TextColor(SailInkBody)
					ui.Text(c, "registry.modelcontextprotocol.io").Font("monospace").FontSize(11.5).TextColor(SailInkMuted)
				})
				u.renderMarketGrid(c, st, registryFiltered, cols, "reg")
			})
		}
	}

	if st.showModal && st.modalItem != nil {
		u.renderMarketInstallModal(c, st, *st.modalItem)
	}
}

func renderMarketFilterPill(c *ui.Context, st *marketPageState, id, label string) {
	if st.filter == id {
		if SailSmallPrimaryButton(c.Key("mkt-flt-"+id), label).Clicked() {
			st.filter = id
		}
		return
	}
	if SailSmallButton(c.Key("mkt-flt-"+id), label).Clicked() {
		st.filter = id
	}
}

func marketMatchesFilter(item apitypes.MarketServer, filter string) bool {
	switch filter {
	case "http":
		return item.Transport == "http"
	case "stdio":
		return item.Transport == "stdio"
	case "installed":
		return item.Installed
	default:
		return true
	}
}

func marketMatchesLocalQuery(item apitypes.MarketServer, q string) bool {
	if !item.Featured {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		item.ID, item.ServerID, item.Title, item.Publisher, item.Description, item.Endpoint, item.Command, strings.Join(item.Args, " "),
	}, " "))
	return strings.Contains(haystack, q)
}

func (u *DesktopUI) renderMarketGrid(c *ui.Context, st *marketPageState, items []apitypes.MarketServer, cols int, prefix string) {
	for start := 0; start < len(items); start += cols {
		end := start + cols
		if end > len(items) {
			end = len(items)
		}
		rowIdx := start / cols
		ui.Row(c).Key(fmt.Sprintf("mkt-row-%s-%d", prefix, rowIdx)).FillWidth().Gap(14).AlignItems(ui.Stretch).Children(func() {
			for i := start; i < end; i++ {
				item := items[i]
				ui.Column(c).Key("mkt-cell-" + prefix + "-" + item.ID).Grow(1).MinWidth(0).Children(func() {
					u.renderMarketCard(c, st, item)
				})
			}
			for pad := end - start; pad < cols; pad++ {
				ui.Box(c).Grow(1).MinWidth(0)
			}
		})
	}
}

func (u *DesktopUI) renderMarketCard(c *ui.Context, st *marketPageState, item apitypes.MarketServer) {
	applyCardElevation(
		ui.Column(c).
			FillWidth().
			Grow(1).
			Padding(16).
			Gap(12).
			Radius(10).
			Background(SailBgSurface),
	).Justify(ui.SpaceBetween).Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				u.renderMarketIcon(c, st, item)
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, item.Title).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						ui.Text(c, item.Publisher).FontSize(11.5).TextColor(SailInkMuted).SingleLine()
						renderMarketChip(c, marketRunsLabel(item.Runs))
						if chip := marketAuthChipLabel(item); chip != "" {
							renderMarketChip(c, chip)
						}
					})
				})
			})

			ui.Text(c, item.Description).
				FontSize(12.5).
				LineHeight(1.45).
				TextColor(SailInkBody).
				MaxLines(2)
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Text(c, item.ServerID).Font("monospace").FontSize(11.5).TextColor(SailInkFaint).SingleLine().Grow(1).MinWidth(0)
			if item.Installed {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					SailQuietStatus(c, i18n.T("common.connected"), SailSuccessInk, SailSuccessInk)
					if SailSmallButton(c.Key("mkt-view-"+item.ID), i18n.T("common.view")).Clicked() {
						u.OpenServiceDetail(item.ServerID)
					}
				})
			} else {
				busy := st.installingID == item.ID
				btnLabel := i18n.T("common.add")
				if marketHasRequiredInputs(item) {
					btnLabel = i18n.T("market.action.add_with_inputs")
				}
				if busy {
					if item.SignIn {
						btnLabel = i18n.T("common.signing_in")
					} else {
						btnLabel = i18n.T("common.adding")
					}
				}
				if SailSmallButton(c.Key("mkt-add-"+item.ID), btnLabel).Disabled(st.installingID != "").Clicked() {
					if marketHasRequiredInputs(item) {
						copyItem := item
						st.modalItem = &copyItem
						st.modalInputs = map[string]string{}
						st.modalErr = ""
						st.showModal = true
					} else {
						u.installFromMarket(st, item, nil)
					}
				}
			}
		})
	})
}

func renderMarketChip(c *ui.Context, label string) {
	ui.Row(c).
		Padding(1, 6).
		Radius(4).
		Background(SailBgMuted).
		AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, label).FontSize(11).FontWeight(500).TextColor(SailInkMuted).SingleLine()
		})
}

// marketRunsLabel localizes the runtime token the backend puts on the wire:
// hosted remotes arrive as the zh-CN placeholder for "remote", while local
// runners (npx, uvx, ...) are already language-neutral.
func marketRunsLabel(runs string) string {
	if runs == i18n.TLang(i18n.LangZH, "market.runs.remote") {
		return i18n.T("market.runs.remote")
	}
	return runs
}

func marketHasRequiredInputs(item apitypes.MarketServer) bool {
	for _, inp := range item.Inputs {
		if inp.Required {
			return true
		}
	}
	return false
}

func marketAuthChipLabel(item apitypes.MarketServer) string {
	if item.SignIn {
		return i18n.T("market.badge.sign_in")
	}
	hasSecret := false
	hasRequired := false
	for _, inp := range item.Inputs {
		if inp.Required {
			hasRequired = true
			if inp.Secret {
				hasSecret = true
			}
		}
	}
	if hasSecret {
		return i18n.T("market.badge.credentials")
	}
	if hasRequired {
		return i18n.T("market.badge.params")
	}
	return ""
}

func (u *DesktopUI) renderMarketIcon(c *ui.Context, st *marketPageState, item apitypes.MarketServer) {
	entry := u.marketIcon(st, item)

	ui.Box(c).
		Size(34, 34).
		Radius(8).
		Background(SailBgSubtle).
		Border(1, SailBorder).
		Clip().
		Center().
		Shrink(0).
		Children(func() {
			if entry != nil && entry.img != nil {
				ui.Image(c, entry.img).Size(34, 34)
				return
			}
			if entry != nil && entry.svg != nil {
				ui.Icon(c, entry.svg).Size(22, 22)
				return
			}
			r, _ := utf8.DecodeRuneInString(strings.TrimSpace(item.Title))
			initial := "?"
			if r != utf8.RuneError && r != 0 {
				initial = strings.ToUpper(string(r))
			}
			ui.Text(c, initial).FontSize(13).FontWeight(700).TextColor(SailInkStrong)
		})
}

// marketIcon shares the cached brand asset between market cards and service headers.
func (u *DesktopUI) marketIcon(st *marketPageState, item apitypes.MarketServer) *marketIconEntry {
	var entry *marketIconEntry
	if item.Icon != "" && u.Client != nil {
		st.iconMu.Lock()
		entry = st.icons[item.Icon]
		if entry == nil {
			entry = &marketIconEntry{loading: true}
			st.icons[item.Icon] = entry
			st.iconMu.Unlock()
			u.loadMarketIconAsync(st, item.ID, item.Icon)
		} else {
			st.iconMu.Unlock()
		}
	}

	return entry
}

func (u *DesktopUI) loadMarketIconAsync(st *marketPageState, id, iconURL string) {
	if u.Client == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		data, ct, err := u.Client.MarketIcon(ctx, id, iconURL)
		var decoded ui.ImageSource
		var parsedSVG *ui.SVG
		if err == nil && len(data) > 0 {
			if strings.Contains(ct, "svg") || strings.Contains(strings.ToLower(iconURL), ".svg") {
				parsedSVG, err = ui.ParseSVG(data)
			} else {
				decoded, err = ui.DecodeImage(data)
			}
		}
		u.updateUI(func() {
			st.iconMu.Lock()
			entry := st.icons[iconURL]
			if entry != nil {
				entry.loading = false
				if err != nil {
					entry.failed = true
				} else {
					entry.img = decoded
					entry.svg = parsedSVG
				}
			}
			st.iconMu.Unlock()
		})
	}()
}

func (u *DesktopUI) renderMarketInstallModal(c *ui.Context, st *marketPageState, item apitypes.MarketServer) {
	ui.DialogBase(c.Key("mkt-install-dialog"), &st.showModal, func(backdrop, panel ui.Element) {
		backdrop.Background(SailModalBackdrop)
		panel.Width(500).
			MaxWidthPercent(92).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Grow(1).MinWidth(0).Children(func() {
				u.renderMarketIcon(c, st, item)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, i18n.T("market.modal.title", item.Title)).FontSize(15).FontWeight(700).TextColor(SailInkStrong).SingleLine()
					ui.Text(c, item.Publisher+" · "+marketRunsLabel(item.Runs)).FontSize(12).TextColor(SailInkMuted).SingleLine()
				})
			})
			if SailSmallButton(c.Key("mkt-modal-close"), i18n.T("common.close")).Clicked() {
				st.showModal = false
			}
		})

		target := item.Endpoint
		if item.Transport == "stdio" {
			target = strings.TrimSpace(item.Command + " " + strings.Join(item.Args, " "))
		}
		ui.Box(c).FillWidth().Padding(8, 10).Radius(6).Background(SailBgSubtle).Border(1, SailBorder).Children(func() {
			ui.Text(c, target).Font("monospace").FontSize(11.5).TextColor(SailInkMuted).SingleLine()
		})

		if st.modalErr != "" {
			SailAlertBox(c, ToneDanger, i18n.T("market.modal.error_title"), st.modalErr)
		}

		for _, inp := range item.Inputs {
			field := inp
			val := st.modalInputs[field.Key]
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					label := field.Label
					if field.Required {
						label += " *"
					}
					ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					renderMarketChip(c, field.Where)
				})
				ti := ui.TextInput(c.Key("mkt-inp-"+item.ID+"-"+field.Key), &val).
					Font("monospace").
					Placeholder(field.Placeholder).
					FillWidth()
				if field.Secret {
					ti.Password()
				}
				if ti.Changed() {
					st.modalInputs[field.Key] = val
				}
				if field.Description != "" {
					ui.Text(c, field.Description).FontSize(11.5).TextColor(SailInkMuted)
				}
			})
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailButton(c.Key("mkt-modal-cancel"), i18n.T("common.cancel")).Disabled(st.installingID != "").Clicked() {
				st.showModal = false
			}
			confirmLabel := i18n.T("market.modal.confirm")
			if st.installingID == item.ID {
				confirmLabel = i18n.T("common.adding")
			}
			if SailPrimaryButton(c.Key("mkt-modal-submit"), confirmLabel).Disabled(st.installingID != "").Clicked() {
				for _, inp := range item.Inputs {
					if inp.Required && strings.TrimSpace(st.modalInputs[inp.Key]) == "" {
						st.modalErr = i18n.T("market.modal.required_missing", inp.Label)
						return
					}
				}
				st.modalErr = ""
				u.installFromMarket(st, item, st.modalInputs)
			}
		})
	})
}

func (u *DesktopUI) fetchMarket(st *marketPageState, query string) {
	if st.loading || u.Client == nil {
		return
	}
	st.loading = true
	st.fetchErr = ""
	st.registryErr = ""
	st.registryQuery = query

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := u.Client.MarketServers(ctx, query)
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.loading = false })
				return err
			}
			u.updateUI(func() {
				st.loading = false
				st.loaded = true
				st.fetchErr = err.Error()
			})
			return nil
		}
		u.updateUI(func() {
			st.loading = false
			st.loaded = true
			st.items = res.Items
			st.registryErr = res.RegistryError
		})
		return nil
	})
}

func (u *DesktopUI) installFromMarket(st *marketPageState, item apitypes.MarketServer, inputs map[string]string) {
	if st.installingID != "" || u.Client == nil {
		return
	}
	st.installingID = item.ID
	copiedInputs := make(map[string]string, len(inputs))
	for k, v := range inputs {
		copiedInputs[k] = v
	}
	spec := item
	req := apitypes.InstallMarketRequest{
		ID:     item.ID,
		Inputs: copiedInputs,
	}
	if !item.Featured {
		req.Server = &spec
	}

	u.RunAsync(func() error {
		timeout := 35 * time.Second
		if item.SignIn {
			timeout = 305 * time.Second
			u.Notify(i18n.T("services.toast.oauth_opened", item.Title))
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		res, err := u.Client.InstallMarketServer(ctx, req)
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.installingID = "" })
				return err
			}
			title, detail := splitAPIError(err)
			msg := title
			if detail != "" {
				msg += i18n.T("market.modal.separator") + detail
			}
			u.updateUI(func() {
				st.installingID = ""
				if st.showModal {
					st.modalErr = msg
				} else {
					st.fetchErr = msg
				}
			})
			return nil
		}

		_ = u.RefreshSync()
		u.updateUI(func() {
			st.installingID = ""
			st.showModal = false
			st.modalItem = nil
			st.modalInputs = map[string]string{}
			for i := range st.items {
				if st.items[i].ID == item.ID || st.items[i].ServerID == res.Server.ID {
					st.items[i].Installed = true
				}
			}
		})
		if res.SyncError != "" {
			u.Notify(fmt.Sprintf(i18n.T("market.toast.added_sync_failed"), item.Title))
		} else {
			u.Notify(fmt.Sprintf(i18n.T("market.toast.installed"), item.Title, res.ToolCount))
		}
		return nil
	})
}
