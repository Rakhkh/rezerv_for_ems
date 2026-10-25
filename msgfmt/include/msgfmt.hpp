#ifndef MSGFMT_HPP
#define MSGFMT_HPP

#include <cstddef>
#include <string>
#include <string_view>
#include <variant>
#include <vector>

namespace msgfmt {

enum class Style {
    bold,
    italic,
    code,
    link,
};

struct Range {
    Style style = Style::bold;
    // Positions refer to UTF-16 code units in the resulting plain text.
    std::size_t offset = 0;
    std::size_t length = 0;
    std::string url;
};

struct ParsedMessage {
    std::string text;
    std::vector<Range> ranges;
};

enum class Error {
    invalid_utf8,
};

using ParseResult = std::variant<ParsedMessage, Error>;
using EscapeResult = std::variant<std::string, Error>;

// Returned strings own their data; neither function retains the input view.
// Parses markup into plain UTF-8 text and sorted UTF-16 style ranges.
// Invalid UTF-8 returns Error::invalid_utf8 without a partial message.
[[nodiscard]] ParseResult parse(std::string_view input);

// Validates UTF-8 and prefixes every service character with a backslash.
// Other bytes are preserved. Invalid UTF-8 returns Error::invalid_utf8.
[[nodiscard]] EscapeResult escape(std::string_view input);

} // namespace msgfmt

#endif // MSGFMT_HPP
