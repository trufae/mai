project('mai-gnome', 'vala', 'c', version: '0.1.0')

vala_args = [
  '--target-glib=2.58',
  '--pkg=gtk4',
  '--pkg=libadwaita-1',
  '--pkg=json-glib-1.0',
  '--pkg=glib-2.0',
  '--pkg=gobject-2.0',
  '--pkg=gio-2.0',
]

dependencies = [
  dependency('gtk4'),
  dependency('libadwaita-1'),
  dependency('json-glib-1.0'),
  dependency('glib-2.0'),
  dependency('gobject-2.0'),
  dependency('gio-2.0'),
]

sources = files(
  'src/main.vala',
  'src/mcp_client.vala',
  'src/chat_window.vala',
  'src/settings_window.vala',
)

executable('mai-gnome', sources, dependencies: dependencies, vala_args: vala_args)