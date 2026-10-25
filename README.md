# msgfmt — часть 1

Библиотека C++17 и консольная программа для преобразования разметки сообщений
в чистый UTF-8-текст и стилевые диапазоны с позициями UTF-16. Поддерживаются
bold, italic, code, ссылки и экранирование текста.

Активное техническое задание: [docs/task1_cplusplus.md](docs/task1_cplusplus.md).
Согласованные решения, этапы и результаты проверок:
[docs/PLAN_task1.md](docs/PLAN_task1.md).
Исходный код и тесты находятся в [msgfmt/](msgfmt/).
Части 2 и 3 в текущую работу не входят.

## Сборка и запуск

Зависимости: компилятор с C++17 и стандартной библиотекой, CMake >= 3.20,
совместимый инструмент сборки. Сторонних библиотек и тестовых фреймворков нет.
Проверенные команды для GCC на Windows, из корня репозитория:

```text
cmake -S msgfmt -B build-gcc -G "MinGW Makefiles" -DCMAKE_CXX_COMPILER=g++ -DCMAKE_BUILD_TYPE=Release
cmake --build build-gcc
ctest --test-dir build-gcc --output-on-failure
```

Для Linux подготовлены команды из ТЗ:

```sh
cmake -S msgfmt -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
printf '%s' 'Привет, **мир**!' | ./build/msgfmt
printf '%s' '2 * 3' | ./build/msgfmt escape
```

Примеры байтового ввода на Windows, описание API, правила разметки и ограничения
классификации Unicode приведены в [msgfmt/README.md](msgfmt/README.md).

## Проверки и инструменты

Реализация и тесты проверены на Windows с GCC 14.2.0 (w64devkit), CMake 4.1.2
и генератором MinGW Makefiles. Release собирается с `-Wall -Wextra` без
предупреждений; все пять групп CTest проходят, включая 51 запуск CLI.
Linux здесь не проверялся. ASan/UBSan не запускались: установленный GCC
не содержит `libasan` и `libubsan`; CMake сообщает об этом при включении опции.

При разработке использовались Codex (ИИ-ассистент), GCC, CMake/CTest, Git
и PowerShell. Python использовался для вспомогательного переноса тестового
кода между файлами; сборка и тесты проекта его не требуют. Git-коммиты
создаёт пользователь после проверки изменений.



# Задание 23

В каталоге service/ реализована консольная служба ghreport на Go: сводка
по открытым issue через GitHub REST API или другой совместимый сервер.
Поддерживаются пагинация, фильтрация PR и даты создания, дедупликация,
ETag-кэш, перенаправления, повторы, частичные отказы и JSON Lines trace.

- [Техническое задание](docs/task23_go_python.md).
- [План и результаты Go-части](docs/PLAN_task2.md).
- [План Python-тестов](docs/PLAN_task3.md).
- [Документация ghreport](service/README.md): параметры, архитектура,
  формат кэша, отчёты, Docker и проверки.
- msgfmt/ — ранее реализованная часть на C++; текущие изменения её не затрагивают.

Go-служба, контейнер и Python-набор части 3 реализованы. В api_tests/ проверяются
CLI, отчёт, сбор и ошибки API, кэш между процессами, повторы, перенаправления,
trace и файловые регрессии через готовый бинарник, а также свойства неизвестного
API. Запуск, результаты Windows/Linux и границы покрытия — в
[README Python-набора](api_tests/README.md). Live-группа проверена на временных
API с разной выдачей; отдельного предоставленного сервера не было.

## Запуск ghreport

Из корня репозитория, Docker не ниже 24 в режиме Linux containers:

```text
docker build -t ghreport service/
docker run --rm ghreport --repos cli/cli --limit 20 --out -
```

При наличии токена в окружении добавьте `-e GITHUB_TOKEN` к docker run.
Другой API задаётся через `-e GITHUB_API_BASE_URL=<адрес>`.
Финальный образ работает от непривилегированного пользователя.

Для запуска без Docker нужен Go не ниже 1.22:

```text
cd service
go run . --repos cli/cli --limit 20 --out -
```

## Проверки Go-службы

Из service/:

```text
go test -count=1 ./...
gofmt -l .
go vet ./...
```

Проверены Windows с Go 1.27.2 и Linux с Go 1.22.12/1.27.3.
Обычный набор использует управляемые локальные API и не требует Docker,
токена или доступа к публичному GitHub. Подробности Linux-прогонов
и отдельной проверки Docker-образа приведены в service/README.md.
