package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// appState — ровно то, что лежит в config.json.
//
// Отдельный тип, а не сам AppConfig: разбор во временное значение не
// оставляет полуприменённых данных, если файл окажется битым, а копировать
// AppConfig нельзя — в нём мьютекс.
type appState struct {
	Configs []AmneziaWGConfig `json:"configs"`
	// SelectedConfigID — конфиг, выбранный пользователем на главном экране.
	// Именно к нему подключается автоподключение при запуске.
	SelectedConfigID string        `json:"selected_config_id,omitempty"`
	Routing          RoutingConfig `json:"routing"`
	Settings         AppSettings   `json:"settings"`
}

// clone возвращает полностью независимую копию состояния.
func (s appState) clone() appState {
	out := s

	if s.Configs != nil {
		out.Configs = make([]AmneziaWGConfig, len(s.Configs))
		for i, cfg := range s.Configs {
			out.Configs[i] = cfg.Clone()
		}
	}
	out.Routing = s.Routing.Clone()

	return out
}

// AppSettings holds user preferences
type AppSettings struct {
	Autoconnect bool `json:"autoconnect"`
}

// AppConfig — состояние приложения и единственная точка доступа к нему.
//
// Поля закрыты намеренно. Отдать наружу срез из-под мьютекса — то же самое,
// что отдать доступ мимо мьютекса: менеджер VPN держит конфигурацию и правила
// всё время соединения и читает их из своих горутин, пока интерфейс их
// правит. Поэтому наружу уходят только глубокие копии, а внутрь — тоже копии
// того, что дал вызывающий.
type AppConfig struct {
	mu    sync.RWMutex
	state appState

	// configPath задаётся при создании и больше не меняется, поэтому читается
	// без блокировки.
	configPath string
}

// NewAppConfig creates a new application configuration
func NewAppConfig(configPath string) *AppConfig {
	return &AppConfig{
		configPath: configPath,
		state: appState{
			Configs: []AmneziaWGConfig{},
			Routing: RoutingConfig{
				Mode:  RoutingModeVPNList,
				Rules: []RoutingRule{},
			},
		},
	}
}

// Path — путь к файлу, в котором хранится состояние.
func (c *AppConfig) Path() string {
	return c.configPath
}

// Load читает состояние с диска.
//
// Битый файл не считается фатальным: он отодвигается в сторону, а приложение
// стартует с настройками по умолчанию. Иначе одна испорченная запись —
// например, оборванная на середине прежней версией — навсегда лишала бы
// пользователя возможности запустить клиент, причём без единой подсказки,
// что делать.
func (c *AppConfig) Load() error {
	data, err := os.ReadFile(c.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No config file yet, use defaults
		}
		return err
	}

	var loaded appState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return c.quarantine(err)
	}

	reparseConfigs(loaded.Configs)
	sanitizeRouting(&loaded.Routing)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = loaded

	return nil
}

// quarantine отодвигает нечитаемый файл и возвращает управление с чистым
// состоянием. Файл именно сохраняется, а не удаляется: в нём приватные ключи,
// восстановить которые больше неоткуда.
func (c *AppConfig) quarantine(cause error) error {
	backup := fmt.Sprintf("%s.corrupt-%s", c.configPath, time.Now().Format("20060102-150405"))

	if err := os.Rename(c.configPath, backup); err != nil {
		// Отодвинуть не вышло — дальше идти нельзя: следующее же сохранение
		// затёрло бы файл, который ещё можно разобрать руками.
		return fmt.Errorf("файл настроек повреждён (%w) и его не удалось сохранить: %v", cause, err)
	}

	log.Printf("Файл настроек повреждён (%v). Он сохранён как %s, приложение запущено с настройками по умолчанию", cause, backup)
	return nil
}

// reparseConfigs заново разбирает сохранённые конфиги из исходного текста.
//
// На диск кладётся уже разобранная структура, поэтому конфиг, добавленный
// старой версией, навсегда остался бы без полей, которых та версия не знала
// (например, параметров AmneziaWG v2). Перечитывание из raw_config подтягивает
// их без ручного пересоздания конфига.
func reparseConfigs(configs []AmneziaWGConfig) {
	for i := range configs {
		raw := configs[i].RawConfig
		if raw == "" {
			continue
		}

		reparsed, err := ParseAmneziaConfig(configs[i].Name, raw)
		if err != nil {
			log.Printf("Конфигурацию %q не удалось перечитать, оставляем как есть: %v", configs[i].Name, err)
			continue
		}

		// Идентификаторы и дату создания сохраняем.
		reparsed.ID = configs[i].ID
		reparsed.CreatedAt = configs[i].CreatedAt
		configs[i] = *reparsed
	}
}

// sanitizeRouting приводит прочитанные с диска правила к пригодному виду.
//
// В отличие от RoutingConfig.Validate, которая отвергает запрос целиком,
// здесь непригодные записи выбрасываются поимённо: файл пользователь вправе
// править руками, и одна опечатка не должна ронять запуск.
func sanitizeRouting(r *RoutingConfig) {
	if !ValidRoutingMode(r.Mode) {
		if r.Mode != "" {
			log.Printf("Неизвестный режим маршрутизации %q — включаем %q", r.Mode, RoutingModeVPNList)
		}
		r.Mode = RoutingModeVPNList
	}

	seen := make(map[string]bool, len(r.Rules))
	kept := r.Rules[:0]

	for i := range r.Rules {
		rule := r.Rules[i]

		if err := rule.Validate(); err != nil {
			log.Printf("Правило маршрутизации отброшено: %v", err)
			continue
		}
		if rule.ID == "" || seen[rule.ID] {
			rule.ID = GenerateID()
		}

		seen[rule.ID] = true
		kept = append(kept, rule)
	}

	r.Rules = kept
}

// Save записывает состояние на диск целиком и неделимо.
func (c *AppConfig) Save() error {
	c.mu.RLock()
	data, err := json.MarshalIndent(c.state, "", "  ")
	c.mu.RUnlock()

	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(c.configPath), 0700); err != nil {
		return err
	}

	return writeFileAtomic(c.configPath, data, 0600)
}

// writeFileAtomic записывает файл так, что читатель видит либо прежнее
// содержимое целиком, либо новое целиком.
//
// Обычная запись поверх сначала обрезает файл: падение или потеря питания в
// этот момент оставляли бы усечённый config.json, то есть потерю всех
// конфигураций вместе с приватными ключами. Сохраняем во временный файл в том
// же каталоге, сбрасываем на диск и переименовываем — переименование в
// пределах файловой системы неделимо.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// Временный файл убираем на любом пути выхода, кроме удачного
	// переименования: иначе после ошибки в каталоге копятся огрызки с
	// приватными ключами.
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Данные обязаны лечь на диск до переименования: иначе после сбоя имя уже
	// новое, а содержимое ещё пустое — ровно то, от чего мы и защищаемся.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""

	// Каталог тоже сбрасываем: без этого сама запись о переименовании может
	// не пережить внезапное выключение.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}

	return nil
}

// AddConfig adds a new WireGuard config
func (c *AppConfig) AddConfig(cfg AmneziaWGConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Configs = append(c.state.Configs, cfg.Clone())
}

// GetConfig returns a config by ID
func (c *AppConfig) GetConfig(id string) *AmneziaWGConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for i := range c.state.Configs {
		if c.state.Configs[i].ID == id {
			found := c.state.Configs[i].Clone()
			return &found
		}
	}
	return nil
}

// UpdateConfig заменяет конфиг по ID, сохраняя идентификатор и дату создания.
func (c *AppConfig) UpdateConfig(id string, cfg AmneziaWGConfig) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.state.Configs {
		if c.state.Configs[i].ID != id {
			continue
		}

		updated := cfg.Clone()
		updated.ID = id
		updated.CreatedAt = c.state.Configs[i].CreatedAt
		c.state.Configs[i] = updated
		return true
	}

	return false
}

// DeleteConfig removes a config by ID
func (c *AppConfig) DeleteConfig(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.state.Configs {
		if c.state.Configs[i].ID == id {
			c.state.Configs = append(c.state.Configs[:i], c.state.Configs[i+1:]...)
			if c.state.SelectedConfigID == id {
				c.state.SelectedConfigID = ""
			}
			return true
		}
	}
	return false
}

// UniqueConfigName делает имя неповторяющимся: одинаковые имена в списке
// не различить глазами. excludeID — конфиг, который это имя уже носит сам
// (нужно при правке, иначе своё же имя считалось бы занятым).
func (c *AppConfig) UniqueConfigName(name, excludeID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	taken := func(candidate string) bool {
		for _, cfg := range c.state.Configs {
			if cfg.ID != excludeID && strings.EqualFold(cfg.Name, candidate) {
				return true
			}
		}
		return false
	}

	if !taken(name) {
		return name
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", name, i)
		if !taken(candidate) {
			return candidate
		}
	}
}

// GetAllConfigs возвращает независимую копию списка конфигураций.
func (c *AppConfig) GetAllConfigs() []AmneziaWGConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]AmneziaWGConfig, len(c.state.Configs))
	for i, cfg := range c.state.Configs {
		out[i] = cfg.Clone()
	}
	return out
}

// SetSelectedConfig запоминает конфиг, выбранный на главном экране.
func (c *AppConfig) SetSelectedConfig(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.SelectedConfigID = id
}

// GetSelectedConfigID возвращает выбранный конфиг, если он ещё существует.
func (c *AppConfig) GetSelectedConfigID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.selectedLocked()
}

// GetAutoconnectConfigID возвращает конфиг для автоподключения: это конфиг,
// выбранный на главном экране. Пустая строка — автоподключение выключено либо
// выбирать пока нечего (конфигов нет), тогда оно просто ничего не делает.
func (c *AppConfig) GetAutoconnectConfigID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.state.Settings.Autoconnect {
		return ""
	}
	return c.selectedLocked()
}

// selectedLocked возвращает выбранный конфиг, если он ещё не удалён.
// Вызывать под c.mu.
func (c *AppConfig) selectedLocked() string {
	for _, cfg := range c.state.Configs {
		if cfg.ID == c.state.SelectedConfigID {
			return cfg.ID
		}
	}
	return ""
}

// GetRouting возвращает независимую копию правил маршрутизации.
func (c *AppConfig) GetRouting() RoutingConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.Routing.Clone()
}

// SetRouting sets the routing configuration
func (c *AppConfig) SetRouting(routing RoutingConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Routing = routing.Clone()
}

// AddRoutingRule adds a routing rule
func (c *AppConfig) AddRoutingRule(rule RoutingRule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Routing.Rules = append(c.state.Routing.Rules, rule)
}

// DeleteRoutingRule removes a routing rule by ID
func (c *AppConfig) DeleteRoutingRule(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	rules := c.state.Routing.Rules
	for i := range rules {
		if rules[i].ID == id {
			c.state.Routing.Rules = append(rules[:i], rules[i+1:]...)
			return true
		}
	}
	return false
}

// SetRoutingMode sets the routing mode
func (c *AppConfig) SetRoutingMode(mode RoutingMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Routing.Mode = mode
}

// GetSettings returns the current settings
func (c *AppConfig) GetSettings() AppSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.Settings
}

// SetSettings updates the settings
func (c *AppConfig) SetSettings(settings AppSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Settings = settings
}
