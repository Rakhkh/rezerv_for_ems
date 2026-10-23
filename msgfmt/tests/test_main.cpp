#include "test_support.hpp"

#include <exception>
#include <type_traits>

static_assert(__cplusplus >= 201703L, "Tests require C++17");
static_assert(std::is_same_v<decltype(&msgfmt::parse),
                             msgfmt::ParseResult (*)(std::string_view)>);
static_assert(std::is_same_v<decltype(&msgfmt::escape),
                             msgfmt::EscapeResult (*)(std::string_view)>);
static_assert(std::is_same_v<decltype(msgfmt::Range::offset), std::size_t>);
static_assert(std::is_same_v<decltype(msgfmt::Range::length), std::size_t>);

int main(int argc, char* argv[])
{
    using namespace msgfmt::tests;

    if (argc == 3 && std::string_view(argv[1]) == "write_cli_fixtures") {
        try {
            write_cli_fixtures(argv[2]);
            return 0;
        } catch (const std::exception& error) {
            std::cerr << error.what() << '\n';
            return 1;
        }
    }
    const std::string_view group = argc == 1 ? "all" : argv[1];
    if (argc > 2 || (group != "all" && group != "utf8"
                     && group != "escape" && group != "parser" && group != "cli_streams")) {
        std::cerr << "Usage: msgfmt_tests [all|utf8|escape|parser|cli_streams]\n";
        return 1;
    }

    TestContext context;
    if (group == "all" || group == "utf8") {
        run_utf8_tests(context);
    }
    if (group == "all" || group == "escape") {
        run_escape_tests(context);
    }
    if (group == "all" || group == "parser") {
        run_parser_tests(context);
    }
    if (group == "all" || group == "cli_streams") {
        run_cli_stream_tests(context);
    }
    if (context.failures != 0) {
        std::cerr << context.failures << " of " << context.checks << " checks failed.\n";
        return 1;
    }
    std::cout << group << ": " << context.checks << " checks passed.\n";
    return 0;
}
