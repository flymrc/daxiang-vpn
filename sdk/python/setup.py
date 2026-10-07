import sys

WHEEL_REFUSAL = "Native SDK wheel platform/signature acceptance is not implemented; wheel distribution is disabled."
if "bdist_wheel" in sys.argv:
    raise RuntimeError(WHEEL_REFUSAL)

from setuptools import Distribution, setup
from wheel.bdist_wheel import bdist_wheel as _bdist_wheel


class BinaryDistribution(Distribution):
    def is_pure(self):
        return False


class PlatformWheel(_bdist_wheel):
    def finalize_options(self):
        # Direct pip/build invocations must obey the same refusal as build.ps1.
        # The old host-derived wheel tag could mislabel a cross-built sidecar.
        raise RuntimeError(WHEEL_REFUSAL)


setup(distclass=BinaryDistribution, cmdclass={"bdist_wheel": PlatformWheel})
