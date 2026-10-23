#include "msgfmt.hpp"

#include <iostream>
#include <string>
#include <string_view>
#include <type_traits>
#include <utility>
#include <variant>

static_assert(__cplusplus >= 201703L, "Tests require C++17");
static_assert(std::is_same_v<decltype(&msgfmt::parse),
                             msgfmt::ParseResult (*)(std::string_view)>);
static_assert(std::is_same_v<decltype(&msgfmt::escape),
                             msgfmt::EscapeResult (*)(std::string_view)>);
static_assert(std::is_same_v<decltype(msgfmt::Range::offset), std::size_t>);
static_assert(std::is_same_v<decltype(msgfmt::Range::length), std::size_t>);

int main()
{
    int failures = 0;
    const auto check = [&failures](bool condition, std::string_view description) {
        if (!condition) {
            std::cerr << "FAIL: " << description << '\n';
            ++failures;
        }
    };

    // Exercise the public value contract without calling unimplemented functions.
    std::string source = u8"\U0001F680 \u043E\u043A";
    msgfmt::ParsedMessage message{
        source,
        {{msgfmt::Style::bold, 3, 2, ""}},
    };
    source.clear();
    check(message.text == u8"\U0001F680 \u043E\u043A", "message owns UTF-8 text");
    check(message.ranges.size() == 1, "message stores ranges");
    const auto& range = message.ranges.front();
    check(range.style == msgfmt::Style::bold && range.offset == 3 && range.length == 2
              && range.url.empty(),
          "range represents style, UTF-16 coordinates and URL");

    const msgfmt::ParseResult parsed = std::move(message);
    check(std::holds_alternative<msgfmt::ParsedMessage>(parsed), "parse success value");
    const msgfmt::ParseResult parse_error = msgfmt::Error::invalid_utf8;
    check(std::get<msgfmt::Error>(parse_error) == msgfmt::Error::invalid_utf8,
          "parse UTF-8 error value");

    const msgfmt::EscapeResult escaped = std::string("a\0b", 3);
    check(std::get<std::string>(escaped).size() == 3, "escape result can own NUL bytes");
    const msgfmt::EscapeResult escape_error = msgfmt::Error::invalid_utf8;
    check(std::get<msgfmt::Error>(escape_error) == msgfmt::Error::invalid_utf8,
          "escape UTF-8 error value");

    if (failures != 0) {
        return 1;
    }
    std::cout << "Public API checks passed.\n";
    return 0;
}
