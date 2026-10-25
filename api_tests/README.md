# Интеграционные тесты ghreport

Набор запускает собранную службу отдельным процессом и проверяет её внешний
контракт из [ТЗ](../docs/task23_go_python.md). Порядок разработки и распределение
проверок — в [PLAN_task3.md](../docs/PLAN_task3.md).

Реализованы первые два этапа: 39 управляемых сценариев CLI, HTTP-запроса,
отчёта, сбора и ошибок API.
Проверяются ошибки аргументов без побочных действий, обязательные параметры
и заголовки, отсутствие/пустое значение/наличие токена, префикс и завершающий
слеш base URL, точные поля отчёта, Unicode, labels, исходная дата, generated_at,
вывод в stdout и файл. Добавлены пагинация с пустой промежуточной страницей,
limit, PR/since, дедупликация, UTC-границы и сортировка с точностью до наносекунд,
порядок репозиториев, частичные и фатальные ошибки, trace этих обращений.
Каждый случай имеет отдельные сервер, рабочий каталог
и кэш; настоящий токен и публичный GitHub для этого не нужны.

Кэш между процессами, повторы, перенаправления, полный набор сценариев trace
и файловые регрессии ещё остаются следующим этапом. Проверки неизвестного API (`live/`) пока
не реализованы. Полное соответствие части 3 на этом этапе не заявляется.

## Окружение и запуск

Python не ниже 3.10, pytest не ниже 7. Единственная прямая зависимость — pytest.
Go не ниже 1.22 нужен для предварительной сборки, Docker — только для отдельной
проверки в Linux. Pytest самостоятельно службу не собирает и образы не запускает.

Из корня репозитория, PowerShell:

```powershell
python -m venv "$env:TEMP\ghreport-api-venv"
& "$env:TEMP\ghreport-api-venv\Scripts\python.exe" -m pip install -r api_tests/requirements.txt
go -C service build -o "$env:TEMP\ghreport.exe" .
$env:GHREPORT_BIN = Join-Path $env:TEMP 'ghreport.exe'
& "$env:TEMP\ghreport-api-venv\Scripts\python.exe" -m pytest api_tests -q
```

С уже выбранным Python-окружением команды короче:

```text
python -m pip install -r api_tests/requirements.txt
python -m pytest api_tests/mock -q
pytest api_tests -q
```

`GHREPORT_BIN` должен быть установлен и во втором примере. Это абсолютный путь
к исполняемому файлу, включая `.exe` на Windows; строка команды, `go run`
и `docker run` вместо пути не поддерживаются. Shell-обёртки не используются.

| Настройка | Поведение mock-набора |
| --- | --- |
| `GHREPORT_BIN` | Обязателен, указывает на заранее собранный бинарник. Отсутствующий, относительный или непригодный путь даёт явную ошибку подготовки/запуска. |
| `GITHUB_API_BASE_URL` | Для каждого mock-запуска заменяется URL собственного сервера. Установка этой переменной не переключает mock-набор на другой API. |
| `GITHUB_TOKEN` | Не наследуется mock-процессом: каждый сценарий явно передаёт синтетический токен, пустое значение или отсутствие переменной. |
| `--api-repos` | Параметр pytest зарегистрирован для будущей live-группы; на текущие mock-проверки не влияет. |

Запуск отдельной проверки или подробный отчёт:

```text
python -m pytest api_tests/mock/test_cli.py -q
python -m pytest api_tests/mock/test_files.py -q
python -m pytest api_tests/mock/test_collection.py -q
python -m pytest api_tests/mock/test_errors.py -q
python -m pytest api_tests -v --durations=10
```

Таймаут обычного процесса — 10 секунд. При превышении процесс завершается
и дожидается, тест падает; частичный вывод и credentials не печатаются помощником.
Обработчики и сокеты сервера очищаются в финализаторе, в том числе после ошибки
утверждения. Ошибки серверного потока передаются pytest и не скрываются.
Общего внутреннего бюджета времени и автоматических повторов тестов нет.
Для отключения служебного кэша самого pytest можно добавить `-p no:cacheprovider`;
это не влияет на отдельные временные кэши ghreport.

## Структура и перенос из Go

- `helpers/runner.py` — запуск процесса, окружение и захват результата.
- `helpers/server.py` — многопоточный HTTP-сервер и журнал запросов.
- `helpers/checks.py` — независимые проверки JSON, отчёта, RFC3339 и JSON Lines;
  точные дроби времени, UTC-смещения и сопоставление trace с журналом сервера.
- Общий `conftest.py` — бинарник, временные пути и фикстура запуска.
- `mock/conftest.py` — фабрика серверов с очисткой и синтетический токен.
- `mock/test_cli.py` — девять вариантов ошибочной конфигурации.
- `mock/test_files.py` — шесть сочетаний API-настроек и назначения отчёта.
- `mock/test_collection.py` — пять сценариев пагинации, отбора, порядка и циклов.
- `mock/test_errors.py` — 19 случаев: ошибки первой/следующей страницы без повторов,
  продолжение сбора, несколько ошибок при частичном успехе, unauthorized/all_failed
  с stdout и файловым out, сохранением старого отчёта и trace.

После проверки Python-эквивалентов на Windows и Linux удалены
`TestCLI/InvalidArguments`, `TestCLI/Report`, `TestAPIClientRequestAndPage`,
`TestCLI/CollectionAndErrors`, `TestCollectIssuesPaginationAndSelection`,
`TestCollectIssuesCycleAndEarlyStop` и сценарий invalid JSON из ResilienceAndTrace.
Из TestAPIClientErrors удалены случаи 304 и неправильной структуры; 5xx пока
остаётся до следующего этапа. Проверки сериализации TestReportJSONContract и
TestFatalReportJSONContract удалены после сверки с внешними проверками отчётов.
Матрица сохранённых утверждений находится в плане. TestOutputFailures,
TestReportContainsToken, таблицы разбора/валидации и TestDockerImage сохраняются.

## Проверка в Linux

При наличии Go и Python в Linux, из корня репозитория:

```bash
go -C service build -o /tmp/ghreport .
export GHREPORT_BIN=/tmp/ghreport
python -m pip install -r api_tests/requirements.txt
python -m pytest api_tests/mock -q
```

Для Docker можно подготовить отдельный тестовый образ. Зависимости загружаются
при подготовке; сам mock-прогон выполняется с `--network none`. Производственный
`service/Dockerfile` не меняется. Пример PowerShell из корня репозитория:

```powershell
$testDockerfile = Join-Path $env:TEMP 'ghreport-api-tests.Dockerfile'
@'
FROM golang:1.22.12-bookworm AS build
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -o /ghreport .
FROM python:3.10-slim-bookworm
RUN python -m pip install --no-cache-dir pytest==7.4.4
COPY --from=build /ghreport /usr/local/bin/ghreport
ENV GHREPORT_BIN=/usr/local/bin/ghreport PYTHONDONTWRITEBYTECODE=1
WORKDIR /repo
'@ | Set-Content -LiteralPath $testDockerfile -Encoding utf8
docker build -f $testDockerfile -t ghreport-api-tests-stage1 service/
$testsPath = (Resolve-Path 'api_tests').Path
docker run --rm --network none --mount "type=bind,source=$testsPath,target=/repo/api_tests,readonly" ghreport-api-tests-stage1 python -m pytest api_tests/mock -q -p no:cacheprovider
```

Pytest внутри контейнера запускает `/usr/local/bin/ghreport` как обычный процесс.
Это проверка Linux-бинарника, а не запуск финального scratch-образа ghreport.
Его свойства по-прежнему проверяет отдельный `TestDockerImage` из Go-набора.
