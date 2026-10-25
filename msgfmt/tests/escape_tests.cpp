#include "test_support.hpp"

#include <array>
#include <variant>

namespace msgfmt::tests {
namespace {

void check_escape(TestContext& context, std::string_view input, std::string_view expected,
                  std::string_view description)
{
    const auto result = msgfmt::escape(input);
    const auto* output = std::get_if<std::string>(&result);
    context.check(output != nullptr, "escape accepts valid UTF-8", description);
    if (output) {
        context.check(*output == expected, "escape exact output bytes", description);
    }
}

void test_escape(TestContext& context)
{
    check_escape(context, {}, "", "empty default view");
    check_escape(context, "2 * 3", R"(2 \* 3)", "specification example");
    check_escape(context, "*_`[]()\\", R"(\*\_\`\[\]\(\)\\)", "all service characters");
    check_escape(context, R"(\*)", R"(\\\*)", "escape an existing escape");
    check_escape(context, R"(\q)", R"(\\q)", "escape every input backslash");
    check_escape(context, "\"/#<>+-={}!", "\"/#<>+-={}!", "other ASCII is unchanged");

    constexpr std::string_view services = "*_`[]()\\";
    for (unsigned int value = 0; value <= 0x7F; ++value) {
        const auto input = bytes({value});
        const auto expected = services.find(input.front()) == std::string_view::npos
            ? input : "\\" + input;
        check_escape(context, input, expected, "every ASCII byte including controls");
    }
    for (const std::size_t count : {1U, 2U, 3U, 16U, 4096U}) {
        check_escape(context, std::string(count, '\\'), std::string(2 * count, '\\'),
                     "consecutive backslashes");
        std::string expected;
        for (std::size_t i = 0; i < count; ++i) {
            expected += R"(\*)";
        }
        check_escape(context, std::string(count, '*'), expected, "consecutive markers");
    }

    const std::string unicode = u8"\uFEFF\u041F\u0440\u0438\u0432\u0435\u0442 "
        u8"\U0001F680 \u4E2D\u6587 \u0439 \u0438\u0306\r\n";
    check_escape(context, unicode, unicode, "preserve Unicode, BOM, normalization and CR/LF");
    check_escape(context, u8"\u00A0\u1680\u2000\u2028\u202F\u3000",
                 u8"\u00A0\u1680\u2000\u2028\u202F\u3000", "preserve Unicode whitespace");
    check_escape(context, std::string("A\0*\r\n", 5), std::string("A\0\\*\r\n", 6),
                 "NUL does not terminate the input");
    const std::string noncharacters = bytes({0xEF, 0xBF, 0xBE, 0xF4, 0x8F, 0xBF, 0xBF});
    check_escape(context, noncharacters, noncharacters, "valid noncharacters remain valid");

    std::string source = "*";
    const auto result = msgfmt::escape(source);
    source.assign("changed");
    const auto* owned = std::get_if<std::string>(&result);
    context.check(owned && *owned == R"(\*)", "escape result owns its bytes");
    const std::string longer = "*" + bytes({0xFF});
    check_escape(context, std::string_view(longer.data(), 1), R"(\*)",
                 "ignore bytes outside the input view");

    for (const auto& item : representative_invalid_inputs()) {
        const std::array<std::string, 3> inputs{
            item.input, "*ok*" + item.input, item.input + "abc",
        };
        for (const auto& input : inputs) {
            const auto escaped = msgfmt::escape(input);
            const auto* error = std::get_if<msgfmt::Error>(&escaped);
            context.check(error && *error == msgfmt::Error::invalid_utf8,
                          "escape returns only an error for invalid UTF-8", item.name);
        }
    }
}

void test_escape_parse_round_trip(TestContext& context)
{
    std::vector<std::string> inputs{
        "", "*_`[]()\\", "**a _b** c_", "[_a]b_](u)", R"([a\]b](a\)b))",
        std::string("a\0*\r\n", 5), u8"🚀 Привет, **мир**! и\u0306 \uFEFF",
        std::string(4096, '\\'), std::string(4096, '['),
    };
    std::string ascii;
    for (unsigned int value = 0; value <= 0x7F; ++value) {
        const auto byte = bytes({value});
        inputs.push_back(byte);
        ascii += byte;
    }
    inputs.push_back(ascii);
    for (const auto value : {char32_t{0x80}, char32_t{0x7FF}, char32_t{0x800}, char32_t{0xD7FF},
                             char32_t{0xE000}, char32_t{0xFFFF}, char32_t{0x10000},
                             char32_t{0x10FFFF}}) {
        inputs.push_back("**" + encode_scalar(value) + "_[]()`\\");
    }
    for (const auto& input : inputs) {
        const auto escaped = msgfmt::escape(input);
        const auto* text = std::get_if<std::string>(&escaped);
        context.check(text != nullptr, "round-trip input is valid UTF-8");
        if (text) {
            check_parse(context, {"parse(escape(x))", *text, input, {}});
        }
    }
}

} // namespace

void run_escape_tests(TestContext& context)
{
    test_escape(context);
    test_escape_parse_round_trip(context);
}

} // namespace msgfmt::tests
