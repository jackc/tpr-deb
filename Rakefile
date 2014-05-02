begin
  require "bundler"
  Bundler.setup
rescue LoadError
  puts "You must `gem install bundler` and `bundle install` to run rake tasks"
end

require "rake/clean"
require "fileutils"
require "rspec/core/rake_task"
require "erb"
require "md2man/roff/engine"

CLOBBER.include("build")

VERSION = File.readlines("backend/main.go").grep(/const version = ".+"/).first[/\d+\.\d+\.\d+/]

namespace :build do
  task :directory do
    Dir.mkdir("build") unless Dir.exists?("build")
  end

  desc "Build assets"
  task assets: :directory do
    sh "cd frontend; middleman build"
  end

  desc "Build tpr binary"
  task binary: "build/tpr"

  desc "Build tpr man page"
  task man: "build/tpr.1.gz"
end

file "build/tpr" => ["build:directory", *FileList["backend/*.go"]] do |t|
  sh "go build -o build/tpr github.com/JackC/tpr/backend"
end

file "build/tpr.1.gz" => "man/tpr.md" do
  md_template = File.read("man/tpr.md")
  md = ERB.new(md_template).result binding
  roff = Md2Man::Roff::ENGINE.render(md)

  # Shelling out to gzip instead of doing it in memory because lintian doesn't
  # consider it to have been done at max compression
  File.write "build/tpr.1", roff
  sh "gzip", "-9", "build/tpr.1"
end

desc "Build all"
task build: ["build:assets", "build:binary", "build:man"]

desc "Run tpr"
task run: "build:binary" do
  puts "Remember to start middleman"
  exec "build/tpr -config config.yml -static-url http://localhost:4567"
end

desc "Watch for source changes and rebuild and rerun"
task :rerun do
  exec "rerun -d backend -p '**/*.*' rake run"
end

task spec_server: "build:binary" do
  FileUtils.mkdir_p "tmp/spec/server"
  FileUtils.touch "tmp/spec/server/stdout.log"
  FileUtils.touch "tmp/spec/server/stderr.log"
  pid = Process.spawn "build/tpr -config=config.test.yml -static-url http://localhost:4567",
    out: "tmp/spec/server/stdout.log",
    err: "tmp/spec/server/stderr.log"
  at_exit { Process.kill "TERM", pid }
end

RSpec::Core::RakeTask.new(:spec)
task spec: :spec_server

desc "Run go tests"
task :test do
  sh "cd backend; go test"
end

task :default => [:test, :spec]

file "tpr_#{VERSION}.deb" => :build do
  pkg_dir = "tpr_#{VERSION}"
  sh "sudo rm -rf #{pkg_dir}"

  FileUtils.cp_r "deploy/ubuntu/template", "#{pkg_dir}"

  control_template = File.read("#{pkg_dir}/DEBIAN/control")
  control = ERB.new(control_template).result binding
  File.write "#{pkg_dir}/DEBIAN/control", control

  FileUtils.rm "#{pkg_dir}/usr/bin/.gitignore"
  FileUtils.rm "#{pkg_dir}/usr/share/tpr/.gitignore"
  FileUtils.rm "#{pkg_dir}/usr/share/man/man1/.gitignore"

  FileUtils.cp "build/tpr", "#{pkg_dir}/usr/bin"
  FileUtils.cp "build/tpr.1.gz", "#{pkg_dir}/usr/share/man/man1"
  FileUtils.cp_r "build/assets", "#{pkg_dir}/usr/share/tpr"
  FileUtils.cp_r "migrate", "#{pkg_dir}/usr/share/tpr/migrate"

  sh "chmod 0755 #{pkg_dir}/usr/bin/tpr"
  sh "chmod 0755 #{pkg_dir}/etc #{pkg_dir}/etc/init #{pkg_dir}/etc/tpr #{pkg_dir}/usr #{pkg_dir}/usr/bin"
  sh "find #{pkg_dir}/etc -type d -exec chmod 0755 {} \\;"
  sh "find #{pkg_dir}/etc -type f -exec chmod 0644 {} \\;"
  sh "find #{pkg_dir}/usr/share -type d -exec chmod 0755 {} \\;"
  sh "find #{pkg_dir}/usr/share -type f -exec chmod 0644 {} \\;"
  sh "sudo chown -R 0:0 #{pkg_dir}"

  sh "dpkg --build #{pkg_dir}"
  sh "lintian #{pkg_dir}.deb"
end

task deb: "tpr_#{VERSION}.deb"
