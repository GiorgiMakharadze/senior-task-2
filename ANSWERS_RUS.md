# Answers

## Q1: Where should the refund HTTP call happen?

**Ответ: D — как отдельный usecase, запускаемый по `SubscriptionCancelledEvent`.**

### Как это реализовано в коде

Usecase отмены коммитит две мутации атомарно в одном `Plan`:
1. Обновление состояния подписки (status → CANCELLED, refundCents, cancelledAt)
2. Вставка в outbox с событием `SubscriptionCancelledEvent`, содержащим сумму возврата

Обе операции выполняются через один вызов `Committer.Apply(ctx, plan)`, поэтому либо обе проходят, либо обе откатываются. Отдельный refund-воркер (не реализован здесь, так как находится вне bounded context подписок) читает outbox и вызывает billing API для возврата средств.

### Анализ вариантов

**Option B (внутри доменного метода `Cancel()`):** Сразу исключается. Домен должен быть чистым — без I/O, HTTP и побочных эффектов. Вызов HTTP внутри агрегата ломает domain purity и делает его нетестируемым без сети.

**Option A (внутри usecase Cancel):** Простой и синхронный подход. Usecase отменяет подписку, коммитит состояние, затем вызывает refund API. Проблема: если API падает после коммита, возникает split-state. Повторный вызов usecase небезопасен, потому что подписка уже отменена. В итоге всё равно нужна компенсационная логика.

**Option C (в сервисном слое после `committer.Apply()`):** Лучше, чем A, потому что состояние уже зафиксировано. Но всё ещё синхронно — клиент блокируется, и при ошибке нужно реализовывать retry прямо в этом слое или терять refund.

**Option D (отдельный usecase по событию):** Наиболее корректный архитектурно подход. Отмена и refund — разные бизнес-процессы:

- Отмена — синхронный state transition (должен быть атомарным)
- Refund — асинхронный сайд-эффект (может падать, требует retry)

Outbox фиксирует событие в той же транзакции. Отдельный воркер:
- обрабатывает событие асинхронно
- делает retry с backoff
- использует идемпотентность
- не блокирует основной поток
- мониторится отдельно

**Цена:** eventual consistency и дополнительная инфраструктура (outbox, воркер, идемпотентность). Для финансовой системы — это ожидаемая сложность.

---

## Q2: If Cancel() works but the refund API is down

### Что должно быть со статусом подписки?

Подписка должна остаться **CANCELLED**. Это бизнес-факт. Refund — это следствие, а не условие. Откатывать отмену из-за временной ошибки API — плохой UX и некорректная модель.

В нашей реализации статус и событие фиксируются атомарно. Outbox гарантирует, что refund будет выполнен позже.

### Нужно ли ретраить?

Да.

Стратегия:
1. Outbox содержит событие с `refundCents`
2. Воркер читает и вызывает API
3. Exponential backoff + jitter (1s, 2s, 4s…)
4. Ограничение попыток (например, 3)
5. Идемпотентность (ключ = subscription ID)

### Если после 3 попыток не получилось?

1. Пометить как `REFUND_FAILED` или отправить в DLQ
2. Поднять alert (финансовая ошибка)
3. Сохранить контекст ошибки
4. Дать возможность ручного retry
5. Запустить reconciliation job

Никогда не игнорировать refund.

---

## Q3: Two problems with the refund calculation

### Problem 1: Почему `time.Since()` — это ошибка?

```go
daysUsed := time.Since(sub.StartDate).Hours() / 24
````

* **Недетерминированность:** результат зависит от текущего времени
* **Невоспроизводимость:** невозможно повторить расчёт
* **Ошибка при реплеях:** используется текущее время, а не бизнес-время
* **Зависимость от окружения:** timezone/clock drift

Решение: внедрить `Clock` и передавать `now`.

---

### Problem 2: Почему float — это опасно?

```go
refundAmount := sub.Price * (30 - daysUsed) / 30
```

* float не хранит точные значения (29.99 ≠ точно 29.99)
* накапливаются ошибки
* возможны финансовые расхождения

---

### Correct implementation

```go
type Clock interface {
    Now() time.Time
}

func (s *Subscription) Cancel(now time.Time) error {
    if s.status == StatusCancelled {
        return ErrSubscriptionAlreadyCancelled
    }

    refundCents := calculateRefundCents(s.priceCents, s.startDate, now)
    s.status = StatusCancelled
    s.cancelledAt = &now
    s.refundCents = refundCents
    return nil
}

func calculateRefundCents(priceCents int64, startDate, now time.Time) int64 {
    const billingPeriodDays = 30

    daysUsed := int64(now.Sub(startDate).Hours()) / 24

    if daysUsed <= 0 {
        return priceCents 
    }
    if daysUsed >= billingPeriodDays {
        return 0
    }
    daysRemaining := billingPeriodDays - daysUsed
    return (priceCents * daysRemaining) / billingPeriodDays
}
```

---

## Q4: Test design for CancelSubscription

### Что проверяют тесты

1. **Happy path:** статус CANCELLED, refund корректен, время совпадает с fake clock
2. **Outbox:** в плане ровно 2 мутации — state + outbox
3. **Already cancelled:** ошибка и отсутствие коммита
4. **Математика:** edge cases (0 дней, 30 дней, 7 дней)

---

### Что мокать

| Dependency | Fake          | Причина                 |
| ---------- | ------------- | ----------------------- |
| Repository | fakeRepo      | возвращает агрегат      |
| Committer  | fakeCommitter | фиксирует Plan          |
| Clock      | fakeClock     | детерминированное время |

---

### Что проверять

* состояние
* атомарность
* корректность события
* ошибки
* точные значения refund

---

## Q5: Business problem of ignoring the validate-customer error

```go
resp, _ := s.client.Get("https://api.billing.com/validate/" + req.CustomerID)
```

Главная проблема — **неверное бизнес-решение при инфраструктурной ошибке**.

Что происходит:

1. HTTP падает → ошибка игнорируется
2. resp = nil → panic или fallback
3. result.Valid = false (zero value)

👉 Валидный клиент отклоняется

Ошибка "не смогли проверить" превращается в "клиент невалиден".

Обратный сценарий тоже возможен — невалидный клиент может пройти.

**Корень проблемы:**
инфраструктурная ошибка → превращается в бизнес-вердикт

**Правильное поведение:**

* вернуть ошибку
* дать ретрай
* никогда не трактовать "не знаю" как "нет"
