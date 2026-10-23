#include "test_support.hpp"

#include <algorithm>
#include <array>
#include <variant>

namespace msgfmt::tests {
namespace {

void test_control_examples(TestContext& context)
{
    for (const auto& item : control_examples()) {
        check_parse(context, item);
    }
}

void test_parser_rules(TestContext& context)
{
    using msgfmt::Style;
    const std::vector<ParseCase> cases{
        {"plain bytes", "a\r\nb", "a\r\nb", {}},
        {"single stars", "*a*", "*a*", {}},
        {"nearest bold close", "**a**b**", "ab**", {{Style::bold, 0, 1, ""}}},
        {"adjacent bold", "**a****b**", "ab",
         {{Style::bold, 0, 1, ""}, {Style::bold, 1, 1, ""}}},
        {"triple stars are not another style", "***a***", "*a*", {{Style::bold, 0, 2, ""}}},
        {"unsuitable bold close is literal", "**a **b**", "a **b", {{Style::bold, 0, 5, ""}}},
        {"empty bold", "****", "****", {}},
        {"empty code", "``", "``", {}},
        {"empty italic", "__", "__", {}},
        {"ASCII letter boundary", "a_b_", "a_b_", {}},
        {"digit boundary", "0_b_", "0_b_", {}},
        {"Russian boundary", u8"я_б_!", u8"я_б_!", {}},
        {"Ukrainian boundary", u8"і_б_!", u8"і_б_!", {}},
        {"Latin classification limitation", u8"é_a_", u8"éa", {{Style::italic, 1, 1, ""}}},
        {"emoji boundary", u8"🚀_ок_", u8"🚀ок", {{Style::italic, 2, 2, ""}}},
        {"punctuation boundary", "(_b_)", "(b)", {{Style::italic, 1, 1, ""}}},
        {"italic cannot close before letter", "_a_b_", "a_b", {{Style::italic, 0, 3, ""}}},
        {"italic cannot close before digit", "_a_9", "_a_9", {}},
        {"italic cannot close before Cyrillic", u8"_a_я", u8"_a_я", {}},
        {"source boundary after markup", "**a**_b_", "ab",
         {{Style::bold, 0, 1, ""}, {Style::italic, 1, 1, ""}}},
        {"all escaped markers", R"(\*\_\`\[\]\(\)\\)", "*_`[]()\\", {}},
        {"backslash before ordinary character", R"(\q)", R"(\q)", {}},
        {"trailing backslash", "x\\", "x\\", {}},
        {"escaped closing marker", R"(**a\*\*b**)", "a**b", {{Style::bold, 0, 4, ""}}},
        {"raw unclosed style", R"(**a \*)", R"(**a \*)", {}},
        {"raw unclosed outer bold", "**a _b_", "**a _b_", {}},
        {"raw unclosed outer italic", "_a **b**", "_a **b**", {}},
        {"keep preceding completed range", "**ok** **bad _x_", "ok **bad _x_",
         {{Style::bold, 0, 2, ""}}},
        {"crossed markers stay raw", "**a _b** c_", "**a _b** c_", {}},
        {"ancestor marker is literal", "**a _b** c_ d**", "a b** c d",
         {{Style::bold, 0, 9, ""}, {Style::italic, 2, 5, ""}}},
        {"same style cannot open below top", "**a _b **c** d_ e**", "a b **c** d e",
         {{Style::bold, 0, 13, ""}, {Style::italic, 2, 9, ""}}},
        {"nested and subsequent ranges", "**a _b_ c** _d_", "a b c d",
         {{Style::bold, 0, 5, ""}, {Style::italic, 2, 1, ""}, {Style::italic, 6, 1, ""}}},
        {"same offset different lengths", "**_a_ b**", "a b",
         {{Style::bold, 0, 3, ""}, {Style::italic, 0, 1, ""}}},
        {"identical intervals bold first", "**_a_**", "a",
         {{Style::bold, 0, 1, ""}, {Style::italic, 0, 1, ""}}},
        {"identical intervals italic first", "_**a**_", "a",
         {{Style::italic, 0, 1, ""}, {Style::bold, 0, 1, ""}}},
        {"code suppresses markup and escapes", "`**a** _b_ [c](u) \\*`", "**a** _b_ [c](u) \\*",
         {{Style::code, 0, 19, ""}}},
        {"backslash cannot escape code closer", "`a\\`b", "a\\b", {{Style::code, 0, 2, ""}}},
        {"nearest code close", "`a`b`", "ab`", {{Style::code, 0, 1, ""}}},
        {"composed Unicode", u8"**й**", u8"й", {{Style::bold, 0, 1, ""}}},
        {"decomposed Unicode", u8"**и\u0306**", u8"и\u0306", {{Style::bold, 0, 2, ""}}},
        {"ZWJ emoji units", u8"**👩\u200D💻**", u8"👩\u200D💻", {{Style::bold, 0, 5, ""}}},
        {"BOM preserved", u8"\uFEFF**a**", u8"\uFEFFa", {{Style::bold, 1, 1, ""}}},
        {"NUL inside bold", std::string("**\0**", 5), std::string(1, '\0'),
         {{Style::bold, 0, 1, ""}}},
        {"NUL and CR/LF", std::string("A\0**b**\r\n", 9), std::string("A\0b\r\n", 5),
         {{Style::bold, 2, 1, ""}}},
        {"multiline bold", "**a\r\nb**", "a\r\nb", {{Style::bold, 0, 4, ""}}},
    };
    for (const auto& item : cases) {
        check_parse(context, item);
    }
}

void test_links(TestContext& context)
{
    using msgfmt::Style;
    const std::vector<ParseCase> cases{
        {"escaped label bracket", R"([a\]b](u))", "a]b", {{Style::link, 0, 3, "u"}}},
        {"escaped URL close", R"([x](a\)b))", "x", {{Style::link, 0, 1, "a)b"}}},
        {"escaped URL parentheses", "[x](a\\(b\\))", "x", {{Style::link, 0, 1, "a(b)"}}},
        {"escaped label underscore", R"([a\_b](u))", "a_b", {{Style::link, 0, 3, "u"}}},
        {"escaped URL underscore", R"([x](a\_b))", "x", {{Style::link, 0, 1, "a_b"}}},
        {"ordinary URL backslash", R"([x](a\qb))", "x", {{Style::link, 0, 1, R"(a\qb)"}}},
        {"escaped URL backslash", R"([x](a\\b))", "x", {{Style::link, 0, 1, R"(a\b)"}}},
        {"raw markup in URL", "[x](u_**[`)", "x", {{Style::link, 0, 1, "u_**[`"}}},
        {"no URL syntax validation", "[x](not-a-url)", "x", {{Style::link, 0, 1, "not-a-url"}}},
        {"URL parentheses not balanced", "[x](a(b)c)", "xc)", {{Style::link, 0, 1, "a(b"}}},
        {"following parentheses are text", "[x](u)(v)", "x(v)", {{Style::link, 0, 1, "u"}}},
        {"empty label", "[](u)", "[](u)", {}},
        {"empty URL", "[a]()", "[a]()", {}},
        {"missing label end", "[a", "[a", {}},
        {"missing URL opener", "[a]u", "[a]u", {}},
        {"missing URL end", "[a](u", "[a](u", {}},
        {"space between label and URL", "[a] (u)", "[a] (u)", {}},
        {"label trailing space", "[a ](u)", "[a ](u)", {}},
        {"URL space", "[x](a b)", "[x](a b)", {}},
        {"backslash does not escape URL space", "[x](a\\ b)", "[x](a\\ b)", {}},
        {"label ranges rolled back", "[**a**]()", "[**a**]()", {}},
        {"label escapes rolled back", R"([a\_b]())", R"([a\_b]())", {}},
        {"unclosed link is atomic", "[**a**](u **b**", "[**a**](u **b**", {}},
        {"link label bold", "[**a**](u)", "a",
         {{Style::link, 0, 1, "u"}, {Style::bold, 0, 1, ""}}},
        {"link inside bold", "**[a](u)**", "a",
         {{Style::bold, 0, 1, ""}, {Style::link, 0, 1, "u"}}},
        {"invalid link inside valid bold", "**[a]()**", "[a]()", {{Style::bold, 0, 5, ""}}},
        {"invalid supplementary link before bold", u8"[🚀]() **x**", u8"[🚀]() x",
         {{Style::bold, 7, 1, ""}}},
        {"resume after invalid label", "[a ](u) **b**", "[a ](u) b", {{Style::bold, 8, 1, ""}}},
        {"resume after invalid URL", "[a](u v) **b**", "[a](u v) b", {{Style::bold, 9, 1, ""}}},
        {"resume after missing URL opener", "[**a**]oops **b**", "[**a**]oops b",
         {{Style::bold, 12, 1, ""}}},
        {"same-style link opener is literal", "[a [b](v)](u)", "a [b](u)",
         {{Style::link, 0, 4, "v"}}},
        {"bare label end inside nested italic", "[_a]b_](u)", "[_a]b_](u)", {}},
        {"code protects label bracket", "[`a]b`](u)", "a]b",
         {{Style::link, 0, 3, "u"}, {Style::code, 0, 3, ""}}},
        {"URL NUL is ordinary", std::string("[x](a\0b)", 8), "x",
         {{Style::link, 0, 1, std::string("a\0b", 3)}}},
    };
    for (const auto& item : cases) {
        check_parse(context, item);
    }
}

void test_markup_whitespace(TestContext& context)
{
    for (const auto value : whitespace) {
        const auto space = encode_scalar(value);
        for (const std::string_view marker : {"**", "_", "`"}) {
            const std::string opening = std::string(marker) + space + "a" + std::string(marker);
            const std::string closing = std::string(marker) + "a" + space + std::string(marker);
            check_parse(context, {"whitespace prevents opening", opening, opening, {}});
            check_parse(context, {"whitespace prevents closing", closing, closing, {}});
        }
        const auto label_start = "[" + space + "a](u)";
        const auto label_end = "[a" + space + "](u)";
        const auto url = "[a](u" + space + ")";
        check_parse(context, {"whitespace prevents link opening", label_start, label_start, {}});
        check_parse(context, {"whitespace prevents label closing", label_end, label_end, {}});
        check_parse(context, {"URL whitespace is invalid", url, url, {}});
        check_parse(context, {"whitespace still preserved in text", "x" + space + "**a**",
                              "x" + space + "a", {{msgfmt::Style::bold, 2, 1, ""}}});
    }
}

std::string wrap_style(msgfmt::Style style, const std::string& text)
{
    switch (style) {
    case msgfmt::Style::bold:
        return "**" + text + "**";
    case msgfmt::Style::italic:
        return "_" + text + "_";
    case msgfmt::Style::code:
        return "`" + text + "`";
    case msgfmt::Style::link:
        return "[" + text + "](u)";
    }
    return {};
}

void test_style_nesting(TestContext& context)
{
    using msgfmt::Style;
    const std::array<Style, 4> styles{Style::bold, Style::italic, Style::code, Style::link};
    for (const auto outer : styles) {
        if (outer == Style::code) {
            continue;
        }
        for (const auto inner : styles) {
            if (inner == outer) {
                continue;
            }
            const auto input = wrap_style(outer, wrap_style(inner, u8"🚀a"));
            check_parse(context, {"pairs of different styles", input, u8"🚀a",
                                  {{outer, 0, 3, outer == Style::link ? "u" : ""},
                                   {inner, 0, 3, inner == Style::link ? "u" : ""}}});
        }
    }
    std::array<Style, 3> outer_styles{Style::bold, Style::italic, Style::link};
    do {
        std::string input = u8"`🚀a`";
        for (auto style = outer_styles.rbegin(); style != outer_styles.rend(); ++style) {
            input = wrap_style(*style, input);
        }
        std::vector<msgfmt::Range> ranges;
        for (const auto style : outer_styles) {
            ranges.push_back({style, 0, 3, style == Style::link ? "u" : ""});
        }
        ranges.push_back({Style::code, 0, 3, ""});
        check_parse(context, {"all four styles", input, u8"🚀a", ranges});
    } while (std::next_permutation(outer_styles.begin(), outer_styles.end()));
}

void test_parse_errors_and_ownership(TestContext& context)
{
    for (const auto& item : representative_invalid_inputs()) {
        const std::array<std::string, 8> inputs{
            item.input, "**ok**" + item.input, "**" + item.input + "**",
            "_" + item.input + "_", "`" + item.input + "`", "[x](" + item.input + ")",
            "[" + item.input + "](u)", "**bad _x_ " + item.input,
        };
        for (const auto& input : inputs) {
            const auto result = msgfmt::parse(input);
            const auto* error = std::get_if<msgfmt::Error>(&result);
            context.check(error && *error == msgfmt::Error::invalid_utf8,
                          "parse validates code, URL, literals and completed prefixes", item.name);
        }
    }
    check_parse(context, {"valid noncharacters", bytes({0xEF, 0xBF, 0xBF}),
                          bytes({0xEF, 0xBF, 0xBF}), {}});
    std::string source = "[**a**](u)";
    const auto result = msgfmt::parse(source);
    source.assign("changed");
    const auto* message = std::get_if<msgfmt::ParsedMessage>(&result);
    context.check(message && message->text == "a" && message->ranges.size() == 2
                      && message->ranges.front().url == "u", "parse owns text and URL");
    const std::string extended = "**a**" + bytes({0xFF});
    const auto bounded = msgfmt::parse(std::string_view(extended.data(), 5));
    const auto* bounded_message = std::get_if<msgfmt::ParsedMessage>(&bounded);
    context.check(bounded_message && bounded_message->text == "a"
                      && bounded_message->ranges.size() == 1, "parse respects view boundary");
    const auto empty = msgfmt::parse({});
    const auto* empty_message = std::get_if<msgfmt::ParsedMessage>(&empty);
    context.check(empty_message && empty_message->text.empty() && empty_message->ranges.empty(),
                  "parse empty default view");
}


void test_long_sequences(TestContext& context)
{
    constexpr std::size_t count = 10000;
    ParseCase valid{"many completed styles", "", "", {}};
    for (std::size_t i = 0; i < count; ++i) {
        valid.input += "**a** ";
        valid.text += "a ";
        valid.ranges.push_back({msgfmt::Style::bold, 2 * i, 1, ""});
    }
    check_parse(context, valid);

    // These italic ranges are provisional until the outer bold is closed.
    // At EOF they must all disappear, while the earlier bold is retained.
    std::string unfinished = "**";
    for (std::size_t i = 0; i < count; ++i) {
        unfinished += u8"_🚀_ ";
    }
    check_parse(context, {"rollback many provisional ranges", "**ok** " + unfinished,
                          "ok " + unfinished, {{msgfmt::Style::bold, 0, 2, ""}}});
}

} // namespace

void run_parser_tests(TestContext& context)
{
    test_control_examples(context);
    test_parser_rules(context);
    test_links(context);
    test_markup_whitespace(context);
    test_style_nesting(context);
    test_parse_errors_and_ownership(context);
    test_long_sequences(context);
}

} // namespace msgfmt::tests
