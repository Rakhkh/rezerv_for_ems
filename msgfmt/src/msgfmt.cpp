#include "msgfmt.hpp"

#include "utf8.hpp"

namespace msgfmt {
namespace {

bool is_service_character(char32_t value) noexcept
{
    switch (value) {
    case U'*':
    case U'_':
    case U'`':
    case U'[':
    case U']':
    case U'(':
    case U')':
    case U'\\':
        return true;
    default:
        return false;
    }
}

} // namespace

EscapeResult escape(std::string_view input)
{
    std::string output;
    output.reserve(input.size());
    std::size_t offset = 0;
    while (offset < input.size()) {
        const auto decoded = detail::decode_utf8(input, offset);
        if (!decoded) {
            return Error::invalid_utf8;
        }
        if (is_service_character(decoded->value)) {
            output.push_back('\\');
        }
        output.append(input.substr(offset, decoded->byte_length));
        offset += decoded->byte_length;
    }
    return output;
}

// parse is implemented in stage 3; no placeholder result is returned.

} // namespace msgfmt
