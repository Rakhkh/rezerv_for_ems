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
    add_executable(msgfmt_tests tests/test_main.cpp)
    target_link_libraries(msgfmt_tests PRIVATE msgfmt_lib)
    msgfmt_configure_target(msgfmt_tests)

    add_test(NAME msgfmt.public_api COMMAND msgfmt_tests)
    add_test(NAME msgfmt.cli_skeleton
        COMMAND "${CMAKE_COMMAND}"
            "-DMSGFMT_EXECUTABLE=$<TARGET_FILE:msgfmt>"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/tests/cli_tests.cmake"
    )
    set_tests_properties(msgfmt.public_api msgfmt.cli_skeleton PROPERTIES TIMEOUT 10)
endif()
