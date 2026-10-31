package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/user/amnezia-web-client/internal/config"
)

// routingApplyDelay — пауза перед применением правок.
//
// Двести миллисекунд: человек за это время не успевает заметить задержку, а
// вставленный список правил или несколько нажатий подряд успевают сложиться
// в одну пересборку.
const routingApplyDelay = 200 * time.Millisecond

// applyRoutingSoon применяет правила на живом туннеле, схлопывая всплеск
// правок в одну пересборку.
//
// Раньше применение шло синхронно прямо в обработчике HTTP, а стоит оно
// дорого: гасится посредник DNS, снимаются все маршруты, весь список
// перебирается заново. Двадцать быстрых правок означали двадцать таких
// пересборок подряд — и двадцать раз браузер заново учил адреса.
//
// Ответ HTTP больше применения не ждёт. Сохранение файла по-прежнему
// синхронно: правило считается принятым, когда оно записано, а не когда
// маршрут встал. Об отказе применения сообщает поток событий — он приходит в
// статусе полем RoutingError.
//
// Потерять отложенное применение при остановке службы не страшно: она
// разбирает туннель целиком, и маршруты уходят вместе с ним.
func (s *Server) applyRoutingSoon() {
	s.routingMu.Lock()
	defer s.routingMu.Unlock()

	// Stop на уже сработавшем таймере возвращает false, и применение в этот
	// момент может идти. Заводим новый: правка, пришедшая во время
	// применения, обязана дойти до следующего.
	if s.routingTimer != nil {
		s.routingTimer.Stop()
	}
	s.routingTimer = time.AfterFunc(s.routingDelay, s.applyRoutingNow)
}

// applyRoutingNow пересобирает маршруты немедленно.
//
// Замок здесь не ради гонки за данными, а ради порядка: два применения,
// пошедшие внахлёст, могли бы лечь в обратном порядке, и в системе осталась
// бы старая маршрутизация. Список правил читается ПОД замком — тот, кто
// дождался, обязан взять уже обновлённый.
func (s *Server) applyRoutingNow() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	routing := s.config.GetRouting()
	if err := s.applyRouting(&routing); err != nil {
		log.Printf("Failed to apply routing: %v", err)
	}
}

// Routing handlers
func (s *Server) handleGetRouting(w http.ResponseWriter, r *http.Request) {
	routing := s.config.GetRouting()
	jsonResponse(w, routing)
}

func (s *Server) handleSetRouting(w http.ResponseWriter, r *http.Request) {
	var routing config.RoutingConfig

	if !decodeJSON(w, r, &routing) {
		return
	}

	// Сюда приходит и загруженный пользователем файл, поэтому содержимое
	// проверяем целиком, а не полагаемся на то, что его собрал интерфейс.
	if err := routing.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.config.SetRouting(routing)
	s.saveConfig()
	s.applyRoutingSoon()

	jsonResponse(w, routing)
}

func (s *Server) handleAddRoutingRule(w http.ResponseWriter, r *http.Request) {
	var rule config.RoutingRule

	if !decodeJSON(w, r, &rule) {
		return
	}

	if err := rule.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.geoProblem(rule); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	rule.ID = config.GenerateID()
	rule.Enabled = true

	s.config.AddRoutingRule(rule)
	s.saveConfig()
	s.applyRoutingSoon()

	jsonResponse(w, rule)
}

// batchRequest — пакет правок списка правил.
//
// Элемент add принимается и объектом, и голой строкой: список, набранный в
// окне, приходит по строке на правило, а вид выводится из значения
// (config.ParseRuleValue). Объект остаётся ради переноса файла с другой
// машины, где вид уже проставлен.
type batchRequest struct {
	Add    []json.RawMessage `json:"add"`
	Remove []string          `json:"remove"`
}

// rejectedRule — правило, которое не взяли, и человеческая причина отказа.
type rejectedRule struct {
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// handleBatchRoutingRules применяет пакет правок разом: одно сохранение файла
// и одна пересборка маршрутов на весь пакет.
//
// Отдельный обработчик, а не цикл вызовов POST /routing/rules, потому что
// пересборка маршрутизации стоит дорого — она гасит посредника DNS и
// перечитывает весь список. Двадцать правил по одному означали двадцать
// пересборок; здесь их одна.
//
// Плохое правило отвергается поштучно, а не валит пакет целиком: список из
// сотни строк, набранный руками, почти наверняка содержит опечатку, и
// возвращать его пользователю целиком незачем — он видит, что именно не
// взяли и почему.
func (s *Server) handleBatchRoutingRules(w http.ResponseWriter, r *http.Request) {
	var req batchRequest

	if !decodeJSON(w, r, &req) {
		return
	}

	candidates, rejected := parseBatchRules(req.Add)
	candidates, rejected = s.checkGeoRules(candidates, rejected)

	added, duplicates, removed := s.config.BatchRoutingRules(candidates, req.Remove)
	for _, rule := range duplicates {
		rejected = append(rejected, rejectedRule{Value: rule.Value, Reason: "уже в списке"})
	}

	// Ничего не изменилось — не трогаем ни файл, ни маршруты: пакет из одних
	// повторов не повод гасить посредника DNS.
	if len(added) > 0 || removed > 0 {
		s.saveConfig()
		s.applyRoutingSoon()
	}

	if added == nil {
		added = []config.RoutingRule{}
	}
	if rejected == nil {
		rejected = []rejectedRule{}
	}

	jsonResponse(w, map[string]any{
		"added":    added,
		"rejected": rejected,
		"removed":  removed,
	})
}

// parseBatchRules разбирает элементы пакета, каждый по отдельности.
func parseBatchRules(items []json.RawMessage) ([]config.RoutingRule, []rejectedRule) {
	var (
		rules    []config.RoutingRule
		rejected []rejectedRule
	)

	for _, item := range items {
		rule, err := parseBatchRule(item)
		if err != nil {
			rejected = append(rejected, rejectedRule{Value: batchRuleLabel(item), Reason: err.Error()})
			continue
		}

		// Повторы внутри самого пакета отсеиваем здесь: хранилище сравнивает
		// с уже сохранённым, а список из окна вполне может содержать одно имя
		// дважды.
		if duplicate := indexOfRule(rules, rule); duplicate >= 0 {
			rejected = append(rejected, rejectedRule{Value: rule.Value, Reason: "повторяется в списке"})
			continue
		}

		rules = append(rules, rule)
	}

	return rules, rejected
}

// parseBatchRule разбирает один элемент пакета — строку или объект.
func parseBatchRule(item json.RawMessage) (config.RoutingRule, error) {
	trimmed := bytes.TrimSpace(item)
	if len(trimmed) == 0 {
		return config.RoutingRule{}, errors.New("пустая запись")
	}

	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return config.RoutingRule{}, errors.New("не разобрать значение")
		}
		return config.ParseRuleValue(value)
	}

	var rule config.RoutingRule
	if err := json.Unmarshal(trimmed, &rule); err != nil {
		return config.RoutingRule{}, errors.New("не разобрать правило")
	}

	// Вид не проставлен — выводим из значения, как для голой строки: файл
	// мог быть собран вручную.
	if rule.Type == "" {
		return config.ParseRuleValue(rule.Value)
	}

	if err := rule.Validate(); err != nil {
		return config.RoutingRule{}, err
	}

	rule.Enabled = true
	return rule, nil
}

// batchRuleLabel достаёт из неразобранного элемента то, что можно показать
// пользователю: без этого отказ выглядел бы как «не взяли что-то».
func batchRuleLabel(item json.RawMessage) string {
	var value string
	if err := json.Unmarshal(item, &value); err == nil {
		return value
	}

	var object struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(item, &object); err == nil && object.Value != "" {
		return object.Value
	}

	return strings.TrimSpace(string(item))
}

// indexOfRule возвращает место такого же правила в списке или -1.
func indexOfRule(rules []config.RoutingRule, rule config.RoutingRule) int {
	for i := range rules {
		if config.SameRule(rules[i], rule) {
			return i
		}
	}
	return -1
}

func (s *Server) handleDeleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	if !s.config.DeleteRoutingRule(id) {
		jsonError(w, "Правило не найдено", http.StatusNotFound)
		return
	}

	s.saveConfig()
	s.applyRoutingSoon()

	jsonResponse(w, map[string]bool{"success": true})
}

func (s *Server) handleSetRoutingMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode config.RoutingMode `json:"mode"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if !config.ValidRoutingMode(req.Mode) {
		jsonError(w, "Неизвестный режим маршрутизации", http.StatusBadRequest)
		return
	}

	s.config.SetRoutingMode(req.Mode)
	s.saveConfig()
	s.applyRoutingSoon()

	jsonResponse(w, map[string]config.RoutingMode{"mode": req.Mode})
}
