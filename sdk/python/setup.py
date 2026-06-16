from setuptools import Distribution, setup
from wheel.bdist_wheel import bdist_wheel as _bdist_wheel


class BinaryDistribution(Distribution):
    def is_pure(self):
        return False


class PlatformWheel(_bdist_wheel):
    def finalize_options(self):
        super().finalize_options()
        self.root_is_pure = False

    def get_tag(self):
        _python, _abi, platform = super().get_tag()
        return "py3", "none", platform


setup(distclass=BinaryDistribution, cmdclass={"bdist_wheel": PlatformWheel})
