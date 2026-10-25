# Интеграционные тесты ghreport

Набор pytest запускает собранный ghreport отдельным процессом и проверяет отчёт,
коды завершения и HTTP-журнал по [спецификации](../docs/SPECIFICATION.md#5-часть-3-интеграционные-тесты-службы-python).

Есть два режима: детерминированные тесты с локальным mock-сервером и live-тесты
с API, данные которого заранее неизвестны. Проверяются пагинация, лимит issue,
PR и since, кэш, повторы, ошибки и формат результата.

## Подготовка и запуск

Нужны Python ≥ 3.10, pytest ≥ 7 и собранный ghreport. Для сборки службы нужен
Go ≥ 1.22. Все команды выполняются из корня репозитория.

Установите зависимости в выбранное Python-окружение (рекомендуется виртуальное):

```sh
python -m pip install -r api_tests/requirements.txt
```

Linux:

```sh
go -C service build -o ../build/ghreport .
export GHREPORT_BIN="$PWD/build/ghreport"
python -m pytest api_tests/mock -q
```

Windows (PowerShell):

```powershell
go -C service build -o ../build/ghreport.exe .
$env:GHREPORT_BIN = (Resolve-Path build/ghreport.exe).Path
python -m pytest api_tests/mock -q
```

Mock-тесты сами запускают локальный сервер. Доступ к GitHub и настоящий токен
для них не нужны; у каждого теста свой временный каталог и кэш.

## Live-тесты

Задайте адрес API и передайте список доступных репозиториев:

```sh
export GITHUB_API_BASE_URL="https://api.github.com"
python -m pytest api_tests/live -q --api-repos cli/cli
```

В PowerShell первая команда: `$env:GITHUB_API_BASE_URL = 'https://api.github.com'`.
Токен при необходимости передаётся через `GITHUB_TOKEN`.

| Настройка | Назначение |
| --- | --- |
| `GHREPORT_BIN` | Абсолютный путь к собранному бинарнику, на Windows — к `.exe` |
| `GITHUB_API_BASE_URL` | Адрес сервера для live-тестов |
| `GITHUB_TOKEN` | Необязательный токен для live-тестов |
| `--api-repos` | Репозитории для live-тестов: `owner/repo[,owner/repo...]` |

Live-тесты проверяют свойства отчёта без привязки к конкретным issue.
Ссылки на страницы и перенаправления должны оставаться на том же сервере
(схема, хост и порт): выход за его пределы обнаруживается по журналу после запроса.

## Полный прогон

```sh
python -m pytest api_tests -q
```

Без `GITHUB_API_BASE_URL` и `--api-repos` live-тесты пропускаются. Для проверки
обоих режимов на своём API задайте URL и добавьте `--api-repos owner/repo`.
Если задана только одна из этих настроек, live-группа завершится ошибкой настройки.

Оба режима можно проверить целиком локально, на временных API с разной выдачей:

```sh
python -m api_tests.verify_live --full
```

Для этой команды нужны те же `GHREPORT_BIN` и Python-окружение с pytest.
