# msgfmt и ghreport

Проект состоит из трёх частей:

| Каталог | Назначение | Требования |
| --- | --- | --- |
| [msgfmt](msgfmt/README.md) | C++-библиотека и CLI: разметка → текст и стилевые диапазоны | C++17, CMake ≥ 3.20 |
| [service](service/README.md) | Go-служба: JSON-отчёт по открытым issue GitHub | Go ≥ 1.22 или Docker ≥ 24 |
| [api_tests](api_tests/README.md) | Интеграционные тесты ghreport | Python ≥ 3.10, pytest ≥ 7, собранный ghreport |

Полное описание требований в [спецификации](docs/SPECIFICATION.md).
Парсер разметки и HTTP-клиент реализованы без готовых Markdown-парсеров
и GitHub SDK.

## Быстрый запуск

Все команды выполняются из корня репозитория. Основные примеры рассчитаны
на Linux; варианты для Windows приведены в README соответствующих частей.

### msgfmt

```sh
cmake -S msgfmt -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
printf '%s' 'Привет, **мир**!' | ./build/msgfmt
```

Результат — JSON с чистым текстом и диапазоном `bold` для слова «мир».
Примеры CLI и подключения библиотеки в [msgfmt/README.md](msgfmt/README.md).

### ghreport через Docker

```sh
docker build -t ghreport service/
docker run --rm ghreport --repos cli/cli --limit 20 --out -
```

Токен необязателен. Если `GITHUB_TOKEN` уже задан в окружении, передайте его
контейнеру флагом `-e GITHUB_TOKEN`. Для другого API задайте
`GITHUB_API_BASE_URL` и добавьте `-e GITHUB_API_BASE_URL`.

Запуск без Docker, параметры и сохранение отчёта описаны
в [service/README.md](service/README.md).

## Проверки

```sh
ctest --test-dir build --output-on-failure
go -C service test ./...
```

Для Python-тестов установите зависимости в выбранное Python-окружение,
соберите ghreport и укажите абсолютный путь к нему:

```sh
python -m pip install -r api_tests/requirements.txt
go -C service build -o ../build/ghreport .
export GHREPORT_BIN="$PWD/build/ghreport"
python -m pytest api_tests -q
```

Без `GITHUB_API_BASE_URL` и `--api-repos` live-тесты пропускаются.
Настройка внешнего API и команды для Windows
в [api_tests/README.md](api_tests/README.md).

## Работа над проектом

- **Затраченное время:** ~14 часов: ~4 часа — генерация кода; ~10 часов — планирование, рефакторинг и ручная проверка.
- **ИИ-инструменты:** Codex. Модель: Sol 6.1; reasoning: high.
- **Инструменты разработки:** GCC, CMake/CTest, Go, Python/pytest, Docker, Git и PowerShell.

## Небольшие комментарии к процессу разработки

Задание оказалось весьма объёмным, ещё и выпало под конец модуля, так что времени на него было не так много. Естественно, большая часть кода сгенерирована. Использовался Codex, модель Sol 6.1 с высоким уровнем размышлений (по тестам OpenAI, у такой конфигурации отличные результаты в чистом программировании). Не стал добавлять в .gitignore файлы, которые использовались агентами. AGENTS.md содержит общие правила ведения разработки, PLAN_task#.md — "черновик", в котором агент фиксировал пройденные и предстоящие этапы, SPECIFICATION.md — просто чуть отформатированное ТЗ. Разработка была разделена на 3 этапа. Каждый этап соответствовал отдельному агенту со своим контекстом. Для рефактора использовались как субагенты, так и полноценная модель на финальных этапах. Git-репозиторий вёл самостоятельно, историю старался сделать чистой, ключевые этапы отмечены тегами.
