# ghreport

Консольная служба на Go для сбора открытых issue GitHub. Исключает pull request,
фильтрует по дате создания и формирует JSON-отчёт в stdout или файл.
Работает с публичным GitHub и совместимыми API.

Запросы выполняются последовательно через стандартную библиотеку Go;
служба обходит страницы, удаляет дубликаты и сортирует выбранные issue
по дате создания и номеру. Контракт в [спецификации](../docs/SPECIFICATION.md#41-назначение).

## Сборка и запуск

Нужен Go ≥ 1.22. Все команды выполняются из корня репозитория.

```sh
go -C service build -o ../build/ghreport .
./build/ghreport --repos cli/cli --limit 20 --cache-dir build/ghreport-cache
```

На Windows (PowerShell):

```powershell
go -C service build -o ../build/ghreport.exe .
.\build\ghreport.exe --repos cli/cli --limit 20 --cache-dir build/ghreport-cache
```

Пример сохранения отчёта и журнала:

```sh
./build/ghreport --repos cli/cli,golang/go --limit 20 --since 2026-01-01 \
  --out build/report.json --trace build/trace.jsonl --cache-dir build/ghreport-cache
```

| Параметр | Назначение | По умолчанию |
| --- | --- | --- |
| `--repos` | Обязательный список `owner/repo` через запятую | — |
| `--limit` | Максимум issue на репозиторий, не меньше 1 | `100` |
| `--since` | Дата создания issue от `YYYY-MM-DD`, включительно, UTC | Без фильтра |
| `--out` | Файл отчёта или `-` для stdout | `-` |
| `--trace` | Файл журнала HTTP в формате JSON Lines | Выключен |
| `--cache-dir` | Каталог постоянного кэша | `./ghreport-cache` |

Отчёт и trace должны записываться в разные файлы. Диагностика идёт в stderr.

## Настройка API

| Переменная окружения | Назначение |
| --- | --- |
| `GITHUB_TOKEN` | Необязательный токен GitHub |
| `GITHUB_API_BASE_URL` | Адрес API; по умолчанию `https://api.github.com` |

Префикс пути и завершающий слеш в адресе API допустимы. Значения читаются
при запуске; для публичных репозиториев можно работать без токена.

## Docker

Нужен Docker ≥ 24 с поддержкой Linux-контейнеров:

```sh
docker build -t ghreport service/
docker run --rm -e GITHUB_TOKEN ghreport --repos cli/cli --limit 20 --out -
```

`-e GITHUB_TOKEN` передаёт уже заданный токен из окружения. Для другого API
задайте `GITHUB_API_BASE_URL` на хосте и добавьте `-e GITHUB_API_BASE_URL`.

Контейнер работает от непривилегированного пользователя. Для сохранения кэша
между запусками добавьте `--mount type=volume,source=ghreport-data,target=/work`
к `docker run` перед именем образа. Без volume файлы удаляются вместе с контейнером.

## Формат отчёта

Пример структуры (данные условные):

```json
{
  "generated_at": "2026-01-05T12:00:00Z",
  "repos": [{
    "repo": "cli/cli", "error": null, "count": 1,
    "issues": [{
      "number": 1, "title": "Пример", "author": "octocat",
      "labels": ["bug"], "created_at": "2026-01-05T10:00:00Z",
      "url": "https://github.com/cli/cli/issues/1"
    }]
  }]
}
```

Порядок репозиториев сохраняется. При ошибке одного репозитория остальные
обрабатываются; у проблемного заполняется `error`, его список issue остаётся пустым.

Коды завершения: **0** — успех, **1** — частичный успех, **2** — неверные
аргументы, **3** — фатальная ошибка. При 401 или ошибках всех репозиториев
в stdout выводится `{"error":"unauthorized"}` или `{"error":"all_failed"}`.

## Кэш и повторы

Кэш хранит страницы ответов и ETag в JSON-файлах. Ответ 304 использует сохранённые
данные и отмечается `from_cache: true` в trace. Один каталог можно использовать
при следующих запусках с другими `--limit` и `--since`.

Таймаут запроса — 30 секунд. Для сетевых ошибок и 5xx выполняются до трёх повторов
с паузами 1, 2 и 4 секунды. Для rate limit — до двух повторов: пауза берётся
из `Retry-After`, затем из времени сброса лимита, иначе составляет 60 и 120 секунд.
Служба представляется API как `User-Agent: ghreport/1.0`.

## Проверки

```sh
go -C service test ./...
go -C service vet ./...
gofmt -l service
```

Процессные проверки с локальным сервером и live-тесты — в [api_tests](../api_tests/README.md).
