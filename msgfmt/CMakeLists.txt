cmake_minimum_required(VERSION 3.20)

project(msgfmt LANGUAGES CXX)

include(CTest)

option(MSGFMT_ENABLE_SANITIZERS "Enable AddressSanitizer and UndefinedBehaviorSanitizer" OFF)

if(MSGFMT_ENABLE_SANITIZERS)
    if(NOT CMAKE_CXX_COMPILER_ID MATCHES "^(GNU|Clang)$")
        message(FATAL_ERROR "MSGFMT_ENABLE_SANITIZERS requires GCC or Clang")
    endif()

    include(CheckCXXSourceCompiles)
    include(CMakePushCheckState)
    cmake_push_check_state(RESET)
    set(CMAKE_REQUIRED_FLAGS "-fsanitize=address,undefined -fno-omit-frame-pointer")
    set(CMAKE_REQUIRED_LINK_OPTIONS "-fsanitize=address,undefined")
    check_cxx_source_compiles("int main() { return 0; }" MSGFMT_SANITIZERS_SUPPORTED)
    cmake_pop_check_state()

    if(NOT MSGFMT_SANITIZERS_SUPPORTED)
        message(FATAL_ERROR
            "AddressSanitizer/UndefinedBehaviorSanitizer cannot be compiled and linked. "
            "Check compiler runtime availability; sanitizers have not been enabled.")
    endif()
endif()

function(msgfmt_configure_target target)
    set_target_properties(${target} PROPERTIES
        CXX_STANDARD 17
        CXX_STANDARD_REQUIRED YES
        CXX_EXTENSIONS NO
    )

    if(CMAKE_CXX_COMPILER_ID MATCHES "^(GNU|Clang)$")
        target_compile_options(${target} PRIVATE -Wall -Wextra)
    endif()

    if(MSGFMT_ENABLE_SANITIZERS)
        target_compile_options(${target} PRIVATE
            -fsanitize=address,undefined -fno-omit-frame-pointer -g
        )
        target_link_options(${target} PRIVATE -fsanitize=address,undefined)
    endif()
endfunction()

add_library(msgfmt_lib STATIC src/utf8.cpp src/msgfmt.cpp)
target_include_directories(msgfmt_lib PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}/include")
target_compile_features(msgfmt_lib PUBLIC cxx_std_17)
msgfmt_configure_target(msgfmt_lib)

add_executable(msgfmt src/main.cpp)
target_link_libraries(msgfmt PRIVATE msgfmt_lib)
msgfmt_configure_target(msgfmt)

if(BUILD_TESTING)
    add_executable(msgfmt_tests
        tests/test_main.cpp
        tests/test_support.cpp
        tests/utf8_tests.cpp
        tests/escape_tests.cpp
        tests/parser_tests.cpp
        tests/cli_tests.cpp
        src/main.cpp
    )
    target_link_libraries(msgfmt_tests PRIVATE msgfmt_lib)
    target_include_directories(msgfmt_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src")
    target_compile_definitions(msgfmt_tests PRIVATE MSGFMT_CLI_TESTING)
    msgfmt_configure_target(msgfmt_tests)

    add_test(NAME msgfmt.utf8 COMMAND msgfmt_tests utf8)
    add_test(NAME msgfmt.escape COMMAND msgfmt_tests escape)
    add_test(NAME msgfmt.parser COMMAND msgfmt_tests parser)
    add_test(NAME msgfmt.cli_streams COMMAND msgfmt_tests cli_streams)
    add_test(NAME msgfmt.cli
        COMMAND "${CMAKE_COMMAND}"
            "-DMSGFMT_EXECUTABLE=$<TARGET_FILE:msgfmt>"
            "-DMSGFMT_TEST_EXECUTABLE=$<TARGET_FILE:msgfmt_tests>"
            "-DMSGFMT_FIXTURE_DIR=${CMAKE_CURRENT_BINARY_DIR}/cli-tests/$<CONFIG>"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/tests/cli_tests.cmake"
    )
    set_tests_properties(msgfmt.utf8 msgfmt.escape msgfmt.parser msgfmt.cli_streams
        PROPERTIES TIMEOUT 10
    )
    set_tests_properties(msgfmt.cli PROPERTIES TIMEOUT 60)
endif()
