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

// Stage 1 declares the API. Definitions follow in stages 2 (escape) and 3 (parse).
// Returned strings own their data; neither function retains the input view.
[[nodiscard]] ParseResult parse(std::string_view input);
[[nodiscard]] EscapeResult escape(std::string_view input);

} // namespace msgfmt

#endif // MSGFMT_HPP
