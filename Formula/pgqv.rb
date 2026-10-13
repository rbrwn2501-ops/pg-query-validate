# This formula lives in the project's main repository, so there is no separate
# homebrew-tap repository. Install it with:
#
#   brew tap serhii-chechun/pg-query-validate https://github.com/serhii-chechun/pg-query-validate
#   brew install pgqv
#
# It installs the prebuilt release binary rather than building from source:
# pgqv is built with cgo, and a Homebrew build has no network access for the Go
# module downloads (and would compile the bundled PostgreSQL C sources).
#
# The sha256 values must match the archives attached to the release. After
# publishing a release, refresh them with scripts/update-formula.sh.
class Pgqv < Formula
  desc "Standalone query validator and linter for PostgreSQL"
  homepage "https://github.com/serhii-chechun/pg-query-validate"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/serhii-chechun/pg-query-validate/releases/download/v1.0.2/pgqv_1.0.2_darwin_arm64.tar.gz"
      sha256 "fe0fb56b77f357ee2f9b2943d60b7053dbbac4641ed3dfb4a15d83d971627942"
    end

    on_intel do
      url "https://github.com/serhii-chechun/pg-query-validate/releases/download/v1.0.2/pgqv_1.0.2_darwin_amd64.tar.gz"
      sha256 "9e6d5d8de916fc6b36a594a346d7cfbf7b1997380e1ce21b4150511c83905ad2"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/serhii-chechun/pg-query-validate/releases/download/v1.0.2/pgqv_1.0.2_linux_arm64.tar.gz"
      sha256 "410f3cb13aaeee00940fb61de4e2d4187c6401293732a800bf5c83b451bf7be5"
    end

    on_intel do
      url "https://github.com/serhii-chechun/pg-query-validate/releases/download/v1.0.2/pgqv_1.0.2_linux_amd64.tar.gz"
      sha256 "b0ce714a06fb244297bbcdc89d505e70c4f92d3a1d97f31a34ad45c5e317ceed"
    end
  end

  def install
    bin.install "pgqv"
  end

  test do
    (testpath/"valid.sql").write "create table t (id uuid primary key);"
    system bin/"pgqv", testpath/"valid.sql"
  end
end
