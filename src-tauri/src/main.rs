// Скрывает консольное окно в Windows-релизе.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    uzi_parser_lib::run()
}
